package api

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/chenjia404/meshproxy/internal/binding"
	"github.com/chenjia404/meshproxy/internal/chat"
	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/discovery"
	"github.com/chenjia404/meshproxy/internal/exit"
	"github.com/chenjia404/meshproxy/internal/ipfsnode"
	"github.com/chenjia404/meshproxy/internal/meshserver"
	"github.com/chenjia404/meshproxy/internal/meshserver/sessionv1"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/publicchannel"
	"github.com/chenjia404/meshproxy/internal/safe"
	"github.com/chenjia404/meshproxy/internal/update"
)

//go:generate go run ../tools/syncconsole
//go:embed console/*
var consoleFS embed.FS

// ChatPageCID 是 /chat 前端的 IPFS CID。請直接在程式碼中更新這個常量。
const ChatPageCID = "QmZyE9d7Th3Eae4P3qK2p6zyGMS7c4aBPG6c7yq1172pp7"

// StatusProvider defines the subset of application state required by the local API.
type StatusProvider interface {
	PeerID() string
	Mode() string
	Socks5Listen() string
	P2PListenAddrs() []string
	StartTime() time.Time
	TrafficStats() TrafficStatsResponse
}

// NodeProvider provides read access to known nodes.
type NodeProvider interface {
	GetAll() []*discovery.NodeDescriptor
}

// CircuitProvider provides read access to circuits.
type CircuitProvider interface {
	ListCircuits() []protocol.CircuitInfo
}

// RelaysProvider provides list of known relay nodes.
type RelaysProvider interface {
	ListRelays() []*discovery.NodeDescriptor
}

// ExitsProvider provides list of known exit nodes.
type ExitsProvider interface {
	ListExits() []*discovery.NodeDescriptor
}

// StreamsProvider provides list of active streams (for API, use client.StreamInfo).
type StreamsProvider interface {
	ListStreams() []StreamInfoResponse
}

// StreamInfoResponse is the API view of a stream (avoids importing client in api).
type StreamInfoResponse struct {
	ID          string `json:"id"`
	CircuitID   string `json:"circuit_id"`
	TargetHost  string `json:"target_host"`
	TargetPort  int    `json:"target_port"`
	State       string `json:"state"`
	RelayPeerID string `json:"relay_peer_id,omitempty"`
	ExitPeerID  string `json:"exit_peer_id,omitempty"`
	HopCount    int    `json:"hop_count,omitempty"`
}

// CircuitInfoResponse enriches protocol.CircuitInfo with derived fields for API.
type CircuitInfoResponse struct {
	ID                  string                `json:"id"`
	State               protocol.CircuitState `json:"state"`
	Plan                protocol.PathPlan     `json:"plan"`
	RelayPeerID         string                `json:"relay_peer_id,omitempty"`
	ExitPeerID          string                `json:"exit_peer_id,omitempty"`
	HopCount            int                   `json:"hop_count"`
	StreamCount         int                   `json:"stream_count"`
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
	BytesSent           uint64                `json:"bytes_sent"`
	BytesReceived       uint64                `json:"bytes_received"`
	LastPingAt          time.Time             `json:"last_ping_at"`
	LastPongAt          time.Time             `json:"last_pong_at"`
	Alive               bool                  `json:"alive"`
	ConsecutiveFailures int                   `json:"consecutive_failures"`
	SmoothedRTTMillis   float64               `json:"smoothed_rtt_ms"`
}

// TrafficStatsResponse summarizes process lifetime proxy traffic.
type TrafficStatsResponse struct {
	BytesSent     uint64 `json:"bytes_sent"`
	BytesReceived uint64 `json:"bytes_received"`
	BytesTotal    uint64 `json:"bytes_total"`
}

// PoolKindStatusResponse is one pool kind's status for API.
type PoolKindStatusResponse struct {
	IdleCount  int `json:"idle_count"`
	InUseCount int `json:"in_use_count"`
	TotalCount int `json:"total_count"`
}

// PoolStatusResponse is the API view of circuit pool status.
type PoolStatusResponse struct {
	Kinds map[string]PoolKindStatusResponse `json:"kinds"`
}

// ScoresProvider provides peer scores (placeholder for future).
type ScoresProvider interface {
	GetScores() any
}

// RecentErrorsProvider provides recent errors for observability.
type RecentErrorsProvider interface {
	GetRecent() []ErrorEntry
}

// MetricsSummaryProvider provides aggregated metrics.
type MetricsSummaryProvider interface {
	GetSummary() map[string]any
}

// ExitSelectionProvider provides read/update of client exit selection config.
type ExitSelectionProvider interface {
	GetExitSelection() config.ExitSelectionConfig
	SetExitSelection(cfg *config.ExitSelectionConfig)
}

// Socks5TunnelProvider provides read/update of the client socks5.tunnel_to_exit flag.
type Socks5TunnelProvider interface {
	GetTunnelToExit() bool
	SetTunnelToExit(enabled bool)
}

// ExitCandidatesProvider returns the current list of exit candidates (after applying selection rules).
type ExitCandidatesProvider interface {
	ListExitCandidates() ([]*discovery.NodeDescriptor, error)
}

// ExitCountryResolver returns the effective country code for an exit descriptor (from ExitInfo or GeoIP). Optional.
type ExitCountryResolver interface {
	CountryForExit(*discovery.NodeDescriptor) string
}

// LocalAPIOpts holds optional providers for extended API endpoints.
type LocalAPIOpts struct {
	Relays              RelaysProvider
	Exits               ExitsProvider
	Streams             StreamsProvider
	Pool                PoolStatusProvider
	CircuitPoolConfig   CircuitPoolConfigProvider
	Scores              ScoresProvider
	Errors              RecentErrorsProvider
	Metrics             MetricsSummaryProvider
	ExitSelection       ExitSelectionProvider
	Socks5Tunnel        Socks5TunnelProvider
	ExitCandidates      ExitCandidatesProvider
	ExitCountryResolver ExitCountryResolver // optional: for resolved country in exit-candidates / display
	// ConfigPath 若非空，保存出口選擇時會寫回該配置文件，使重啟後仍生效。
	ConfigPath string
	// ExitService 僅在 mode=relay+exit 時非空，用於出口策略/狀態 API。
	ExitService *exit.Service
	// ChatService provides first-stage direct chat APIs.
	ChatService    ChatProvider
	PublicChannels PublicChannelProvider
	MeshServer     MeshServerProvider
	UpdateService  UpdateProvider
	UpdateSettings UpdateSettingsProvider
	Identity       IdentityProvider
	// IPFS 嵌入式閘道與 API（可選）；非空且設定啟用時註冊 /ipfs/ 與 /api/ipfs/*。
	IPFS *ipfsnode.EmbeddedIPFS
}

type UpdateProvider interface {
	CheckForUpdate(ctx context.Context) (update.Info, error)
	ApplyUpdate(ctx context.Context) (update.ApplyResult, error)
}

type UpdateSettingsProvider interface {
	GetAutoUpdate() bool
	SetAutoUpdate(enabled bool) error
}

type IdentityProvider interface {
	ExportIdentityPrivateKeyBase58() (privateKeyBase58 string, peerID string, err error)
	ImportIdentityPrivateKeyBase58(encoded string) (peerID string, err error)
	SignChallenge(challenge string) (signatureBase64 string, publicKeyBase64 string, peerID string, err error)
}

// MeshServerProvider exposes centralized server chat APIs.
type MeshServerProvider interface {
	ListConnections() []meshserver.ConnectionInfo
	// ListConnectedServers returns the persisted list of meshserver connections.
	ListConnectedServers() []meshserver.ConnectionInfo
	// Legacy stable-id mapping APIs are kept for backward compatibility, but
	// meshserver now uses numeric space_id/channel_id natively.
	GetOrCreateSpaceID(serverID string) (int, error)
	GetServerIDBySpaceID(spaceID int) (string, error)
	GetOrCreateChannelID(channelID string) (int, error)
	GetChannelIDByChannelIDIntID(channelIDIntID int) (string, error)
	Connect(ctx context.Context, name, peerID, clientAgent, protocolID string) (meshserver.ConnectionInfo, error)
	Disconnect(name string) error
	ListServers(ctx context.Context, connection string) (*sessionv1.ListSpacesResp, error)
	// CreateSpace creates a new space on the meshserver (CREATE_SPACE).
	CreateSpace(ctx context.Context, connection, name, description string, visibility sessionv1.Visibility, allowChannelCreation bool) (*sessionv1.CreateSpaceResp, error)
	JoinServer(ctx context.Context, connection, serverID string) (*sessionv1.JoinSpaceResp, error)
	InviteServerMember(ctx context.Context, connection, serverID, targetUserID string) (*sessionv1.InviteSpaceMemberResp, error)
	KickServerMember(ctx context.Context, connection, serverID, targetUserID string) (*sessionv1.KickSpaceMemberResp, error)
	BanServerMember(ctx context.Context, connection, serverID, targetUserID string) (*sessionv1.BanSpaceMemberResp, error)
	ListServerMembers(ctx context.Context, connection, serverID string, afterMemberID uint64, limit uint32) (*sessionv1.ListSpaceMembersResp, error)
	ListChannels(ctx context.Context, connection, serverID string) (*sessionv1.ListChannelsResp, error)
	CreateGroup(ctx context.Context, connection, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateGroupResp, error)
	CreateChannel(ctx context.Context, connection, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateChannelResp, error)
	SubscribeChannel(ctx context.Context, connection, channelID string, lastSeenSeq uint64) (*sessionv1.SubscribeChannelResp, error)
	UnsubscribeChannel(ctx context.Context, connection, channelID string) (*sessionv1.UnsubscribeChannelResp, error)
	SendText(ctx context.Context, connection, channelID, clientMsgID, text string) (*sessionv1.SendMessageAck, error)
	// SendMessage sends text/image/file/system message to a channel.
	// For images/files, caller should pass MediaImage/MediaFile with InlineData.
	SendMessage(ctx context.Context, connection, channelID, clientMsgID string, messageType sessionv1.MessageType, text string, images []*sessionv1.MediaImage, files []*sessionv1.MediaFile) (*sessionv1.SendMessageAck, error)
	// GetMedia fetches an image/file by media_id and returns the full inline payload.
	GetMedia(ctx context.Context, connection, mediaID string) (*sessionv1.MediaFile, error)
	SyncChannel(ctx context.Context, connection, channelID string, afterSeq uint64, limit uint32) (*sessionv1.SyncChannelResp, error)
	// GetMyPermissions returns the caller's permissions in the given server.
	// It is exposed for front-end via a HTTP endpoint that corresponds to GET_MY_PERMISSIONS.
	GetMyPermissions(ctx context.Context, connection, serverID string) (*meshserver.MyPermissions, error)
	// ListMyServers returns servers where the caller is already a member.
	// It is exposed for front-end via a HTTP endpoint.
	ListMyServers(ctx context.Context, connection string) (*meshserver.MyServersResp, error)
	// ListMyGroups returns joined groups (GROUP-type channels) for a space.
	// It is exposed for front-end via a HTTP endpoint.
	ListMyGroups(ctx context.Context, connection, spaceID string) (*meshserver.MyGroupsResp, error)

	// GetCreateSpacePermissions returns the caller's permissions to create spaces on this meshserver.
	// This corresponds to GET_CREATE_SPACE_PERMISSIONS.
	GetCreateSpacePermissions(ctx context.Context, connection string) (*sessionv1.GetCreateSpacePermissionsResp, error)

	// FetchMeshServerHTTPAccessToken 以本機 libp2p 身份對使用者指定的 meshserver HTTP 基底 URL 完成
	// POST /v1/auth/challenge 與 /v1/auth/verify，取得 JWT（與 meshserver 官方 HTTP API 一致）。
	FetchMeshServerHTTPAccessToken(ctx context.Context, baseURL, protocolID string) (*meshserver.HTTPAccessTokenResult, error)
}

// ChatProvider exposes direct chat functions to the local API.
type ChatProvider interface {
	GetProfile() (chat.Profile, error)
	UpdateProfile(nickname, bio string) (chat.Profile, error)
	UpdateProfileAvatar(fileName string, data []byte) (chat.Profile, error)
	AvatarPath(fileName string) (string, error)
	ListContacts() ([]chat.Contact, error)
	UpdateContactNickname(peerID, nickname string) (chat.Contact, error)
	SetContactBlocked(peerID string, blocked bool) (chat.Contact, error)
	ListRequests() ([]chat.Request, error)
	SendRequest(toPeerID, introText string) (chat.Request, error)
	AcceptRequest(requestID string) (chat.Conversation, error)
	RejectRequest(requestID string) error
	ListConversations() ([]chat.Conversation, error)
	// ClearConversationUnreadCount sets unread_count to 0 for a conversation (local UI state).
	ClearConversationUnreadCount(conversationID string) (chat.Conversation, error)
	// DeleteConversationLocal removes one direct chat and all local messages (no network).
	DeleteConversationLocal(conversationID string) error
	// DeleteContactLocal removes local conversations/messages with that peer and deletes the contact row + requests.
	DeleteContactLocal(peerID string) error
	UpdateConversationRetention(conversationID string, minutes int) (chat.Conversation, error)
	ListMessages(conversationID string) ([]chat.Message, error)
	ListMessagesPage(conversationID string, limit, offset int) ([]chat.Message, int, error)
	SyncConversation(conversationID string) error
	RevokeMessage(conversationID, msgID string) error
	SendFile(conversationID, fileName, mimeType string, data []byte, uploadToIPFS bool) (chat.Message, error)
	GetMessageFile(conversationID, msgID string) (chat.Message, []byte, error)
	SendText(conversationID, text string) (chat.Message, error)
	ConnectPeer(peerID string) error
	PeerStatus(peerID string) (map[string]any, error)
	NetworkStatus() map[string]any
	ListGroups() ([]chat.Group, error)
	GetGroupDetails(groupID string) (chat.GroupDetails, error)
	CreateGroup(title string, members []string) (chat.Group, error)
	InviteGroupMember(groupID, peerID, role, inviteText string) (chat.Group, error)
	AcceptGroupInvite(groupID string) (chat.Group, error)
	LeaveGroup(groupID, reason string) (chat.Group, error)
	RemoveGroupMember(groupID, peerID, reason string) (chat.Group, error)
	UpdateGroupTitle(groupID, title string) (chat.Group, error)
	UpdateGroupRetention(groupID string, minutes int) (chat.Group, error)
	DissolveGroup(groupID, reason string) (chat.Group, error)
	TransferGroupController(groupID, peerID string) (chat.Group, error)
	ListGroupMessages(groupID string) ([]chat.GroupMessage, error)
	RevokeGroupMessage(groupID, msgID string) error
	SendGroupText(groupID, text string) (chat.GroupMessage, error)
	SendGroupFile(groupID, fileName, mimeType string, data []byte) (chat.GroupMessage, error)
	GetGroupMessageFile(groupID, msgID string) (chat.GroupMessage, []byte, error)
	SyncGroup(groupID, fromPeerID string) error
	CreateBindingDraft(ethAddress string, chainID uint64, ttlSeconds int64) (binding.BindingDraft, error)
	FinalizeBindingRecord(payload binding.BindingPayload, peerSignature, ethSignature string) (chat.Profile, error)
	ClearLocalBinding() (chat.Profile, error)
	GetLocalPeerProfile() (chat.Profile, error)
	GetContactBindingDetails(peerID string) (chat.ContactBindingDetails, error)
	Close() error
}

type PublicChannelProvider interface {
	CreateChannel(input publicchannel.CreateChannelInput) (publicchannel.ChannelSummary, error)
	CreateChannelWithAvatar(name, bio string, messageRetentionMinutes int, fileName, mimeType string, data []byte) (publicchannel.ChannelSummary, error)
	UpdateChannelProfile(channelID string, input publicchannel.UpdateChannelProfileInput) (publicchannel.ChannelSummary, error)
	UpdateChannelProfileWithAvatar(channelID, name, bio string, messageRetentionMinutes int, fileName, mimeType string, data []byte) (publicchannel.ChannelSummary, error)
	CreateChannelMessage(channelID string, input publicchannel.UpsertMessageInput) (publicchannel.ChannelMessage, error)
	CreateChannelFileMessage(channelID, text, fileName, mimeType string, data []byte) (publicchannel.ChannelMessage, error)
	CreateChannelFilesMessage(channelID, text string, files []publicchannel.UploadFileInput) (publicchannel.ChannelMessage, error)
	UpdateChannelMessage(ctx context.Context, channelID string, messageID int64, input publicchannel.UpsertMessageInput) (publicchannel.ChannelMessage, error)
	DeleteChannelMessage(ctx context.Context, channelID string, messageID int64) (publicchannel.ChannelMessage, error)
	GetChannelSummary(channelID string) (publicchannel.ChannelSummary, error)
	GetChannelHead(channelID string) (publicchannel.ChannelHead, error)
	GetChannelMessages(channelID string, beforeMessageID int64, limit int) ([]publicchannel.ChannelMessage, error)
	GetChannelMessage(channelID string, messageID int64) (publicchannel.ChannelMessage, error)
	GetChannelChanges(channelID string, afterSeq int64, limit int) (publicchannel.GetChangesResponse, error)
	ListChannelsByOwner(ownerPeerID string) ([]publicchannel.ChannelSummary, error)
	ListSubscribedChannels() ([]publicchannel.ChannelSummary, error)
	ListProviders(channelID string) ([]publicchannel.ChannelProvider, error)
	ClearChannelUnreadCount(channelID string) (publicchannel.ChannelSummary, error)
	SubscribeChannel(ctx context.Context, channelID string, seedPeerIDs []string, lastSeenSeq int64) (publicchannel.SubscribeResult, error)
	SubscribeChannelAsync(channelID string, seedPeerIDs []string, lastSeenSeq int64) (publicchannel.SubscribeResult, error)
	UnsubscribeChannel(channelID string) error
	SyncChannel(ctx context.Context, channelID string) error
	LoadMessagesFromProviders(ctx context.Context, channelID string, beforeMessageID int64, limit int) ([]publicchannel.ChannelMessage, error)
}

// PoolStatusProvider returns current circuit pool status.
type PoolStatusProvider interface {
	GetPoolStatus() *PoolStatusResponse
}

// CircuitPoolConfigProvider reads and updates runtime circuit pool configuration.
type CircuitPoolConfigProvider interface {
	GetPoolConfig() *config.CircuitPoolConfig
	SetPoolTotalLimits(minTotal, maxTotal int) bool
}

// LocalAPI exposes HTTP API for node status, nodes, circuits, relays, exits, streams, pool, scores, errors, metrics.
type LocalAPI struct {
	statusProvider  StatusProvider
	nodeProvider    NodeProvider
	circuitProvider CircuitProvider
	opts            *LocalAPIOpts
	server          *http.Server
	consoleHTML     []byte // embedded console index.html, served directly to avoid redirect
	statusMu        sync.RWMutex
	statusCache     map[string]any
	statusCacheAt   time.Time
}

func (a *LocalAPI) meshSpaceIntID(spaceID uint32) int {
	if spaceID == 0 {
		return 0
	}
	return int(spaceID)
}

func (a *LocalAPI) meshChannelIntID(channelID uint32) int {
	if channelID == 0 {
		return 0
	}
	return int(channelID)
}

// NewLocalAPI creates a new LocalAPI instance. opts may be nil for minimal API.
func NewLocalAPI(listen string, sp StatusProvider, np NodeProvider, cp CircuitProvider, opts *LocalAPIOpts) *LocalAPI {
	mux := http.NewServeMux()
	api := &LocalAPI{
		statusProvider:  sp,
		nodeProvider:    np,
		circuitProvider: cp,
		opts:            opts,
	}
	mux.HandleFunc("/api/v1/status", api.handleStatus)
	mux.HandleFunc("/api/v1/nodes", api.handleNodes)
	mux.HandleFunc("/api/v1/relays", api.handleRelays)
	mux.HandleFunc("/api/v1/exits", api.handleExits)
	mux.HandleFunc("/api/v1/circuits", api.handleCircuits)
	mux.HandleFunc("/api/v1/streams", api.handleStreams)
	mux.HandleFunc("/api/v1/scores", api.handleScores)
	mux.HandleFunc("/api/v1/errors/recent", api.handleErrorsRecent)
	mux.HandleFunc("/api/v1/metrics/summary", api.handleMetricsSummary)
	mux.HandleFunc("/api/v1/client/exit-selection", api.handleExitSelection)
	mux.HandleFunc("/api/v1/client/exit-candidates", api.handleExitCandidates)
	mux.HandleFunc("/api/v1/client/circuit-pool", api.handleCircuitPoolConfig)
	mux.HandleFunc("/api/v1/chat/me", api.handleChatMe)
	mux.HandleFunc("/api/v1/chat/profile", api.handleChatProfile)
	mux.HandleFunc("/api/v1/chat/profile/avatar", api.handleChatProfileAvatar)
	mux.HandleFunc("/api/v1/chat/profile/binding/draft", api.handleChatProfileBindingDraft)
	mux.HandleFunc("/api/v1/chat/profile/binding", api.handleChatProfileBinding)
	mux.HandleFunc("/api/v1/chat/avatars/", api.handleChatAvatar)
	mux.HandleFunc("/api/v1/chat/contacts", api.handleChatContacts)
	mux.HandleFunc("/api/v1/chat/contacts/", api.handleChatContactItem)
	mux.HandleFunc("/api/v1/chat/requests", api.handleChatRequests)
	mux.HandleFunc("/api/v1/chat/requests/", api.handleChatRequestItem)
	mux.HandleFunc("/api/v1/chat/conversations", api.handleChatConversations)
	mux.HandleFunc("/api/v1/chat/conversations/", api.handleChatConversationItem)
	mux.HandleFunc("/api/v1/chat/ws", api.handleChatWebSocket)
	mux.HandleFunc("/api/v1/chat/network/status", api.handleChatNetworkStatus)
	mux.HandleFunc("/api/v1/chat/peers/", api.handleChatPeerRoutes)
	mux.HandleFunc("/api/v1/identity/private-key/export", api.handleIdentityPrivateKeyExport)
	mux.HandleFunc("/api/v1/identity/private-key/import", api.handleIdentityPrivateKeyImport)
	mux.HandleFunc("/api/v1/identity/challenge/sign", api.handleIdentityChallengeSign)
	mux.HandleFunc("/api/v1/groups", api.handleGroups)
	mux.HandleFunc("/api/v1/groups/", api.handleGroupItem)
	mux.HandleFunc("/api/v1/public-channels", api.handlePublicChannels)
	mux.HandleFunc("/api/v1/public-channels/subscriptions", api.handlePublicChannelSubscriptions)
	mux.HandleFunc("/api/v1/public-channels/", api.handlePublicChannelItem)
	mux.HandleFunc("/api/v1/meshserver/spaces", api.handleMeshServerServers)
	mux.HandleFunc("/api/v1/meshserver/spaces/", api.handleMeshServerServerItem)
	mux.HandleFunc("/api/v1/meshserver/servers", api.handleMeshServerConnectedServers)
	mux.HandleFunc("/api/v1/meshserver/my_servers", api.handleMeshServerMyServers)
	mux.HandleFunc("/api/v1/meshserver/media/", api.handleMeshServerMediaItem)
	mux.HandleFunc("/api/v1/meshserver/channels/", api.handleMeshServerChannelItem)
	mux.HandleFunc("/api/v1/meshserver/connections", api.handleMeshServerConnections)
	mux.HandleFunc("/api/v1/meshserver/connections/", api.handleMeshServerConnectionItem)
	mux.HandleFunc("/api/v1/meshserver/server/my_permissions", api.handleMeshServerServerMyPermissions)
	mux.HandleFunc("/api/v1/meshserver/http/access_token", api.handleMeshServerHTTPAccessToken)
	mux.HandleFunc("/api/v1/update/check", api.handleUpdateCheck)
	mux.HandleFunc("/api/v1/update/apply", api.handleUpdateApply)
	mux.HandleFunc("/api/v1/update/settings", api.handleUpdateSettings)
	if opts != nil && opts.ExitService != nil && opts.ExitService.Policy != nil {
		mux.HandleFunc("/api/v1/exit/policy", api.handleExitPolicy)
		mux.HandleFunc("/api/v1/exit/status", api.handleExitStatus)
		mux.HandleFunc("/api/v1/exit/drain", api.handleExitDrain)
		mux.HandleFunc("/api/v1/exit/resume", api.handleExitResume)
	}

	if opts != nil && opts.IPFS != nil {
		if opts.IPFS.GatewayEnabled() {
			mux.HandleFunc("/ipfs", api.serveIPFSGateway)
			mux.HandleFunc("/ipfs/", api.serveIPFSGateway)
		}
		if opts.IPFS.APIEnabled() {
			mux.HandleFunc("/api/ipfs/add", api.handleIPFSAdd)
			mux.HandleFunc("/api/ipfs/add-dir", api.handleIPFSAddDir)
			mux.HandleFunc("/api/ipfs/stat/", api.handleIPFSStat)
			mux.HandleFunc("/api/ipfs/pin/", api.handleIPFSPin)
		}
	}

	// Console: serve index.html directly from embed (no FileServer, no redirect)
	var consoleHTML []byte
	if sub, err := fs.Sub(consoleFS, "console"); err == nil {
		consoleHTML, _ = fs.ReadFile(sub, "index.html")
	}
	api.consoleHTML = consoleHTML

	// Wrap mux to serve /console and /console/ with 200 + body (never redirect)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applyCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		path := r.URL.Path
		if path == "/console" || path == "/console/" {
			if len(api.consoleHTML) > 0 {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				w.Write(api.consoleHTML)
				return
			}
			http.NotFound(w, r)
			return
		}
		if path == "/chat" || path == "/chat/" {
			api.handleChatPage(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	api.server = &http.Server{
		Addr:    listen,
		Handler: handler,
	}
	return api
}

func (a *LocalAPI) handleChatPage(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(ChatPageCID) == "" {
		http.Error(w, "chat page cid is not configured", http.StatusServiceUnavailable)
		return
	}
	if a.opts == nil || a.opts.IPFS == nil || !a.opts.IPFS.GatewayEnabled() {
		http.Error(w, "chat page cid requires ipfs gateway", http.StatusServiceUnavailable)
		return
	}
	target := "/ipfs/" + strings.TrimSpace(ChatPageCID) + "/"
	if rawQuery := r.URL.RawQuery; rawQuery != "" {
		target += "?" + rawQuery
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func applyCORSHeaders(w http.ResponseWriter, r *http.Request) {
	headers := w.Header()
	headers.Set("Access-Control-Allow-Origin", "*")
	headers.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
		headers.Set("Access-Control-Allow-Headers", reqHeaders)
	} else {
		headers.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept, Origin, X-Requested-With")
	}
	headers.Set("Access-Control-Expose-Headers", "Content-Type, Content-Length, Content-Disposition")
	headers.Set("Access-Control-Max-Age", "86400")
}

// Start launches the HTTP server in a separate goroutine.
func (a *LocalAPI) Start() {
	go func() {
		log.Printf("[api] listening on %s", a.server.Addr)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[api] server error: %v", err)
		}
	}()
}

// Shutdown gracefully stops the HTTP server.
func (a *LocalAPI) Shutdown() error {
	if a.server == nil {
		return nil
	}
	return a.server.Close()
}

func (a *LocalAPI) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// /status may be polled frequently by UI. Cache a short snapshot to avoid
	// repeatedly collecting optional heavy fields on every request.
	const statusCacheTTL = 1200 * time.Millisecond
	now := time.Now()

	a.statusMu.RLock()
	if a.statusCache != nil && now.Sub(a.statusCacheAt) <= statusCacheTTL {
		resp := cloneStatusMap(a.statusCache)
		a.statusMu.RUnlock()
		writeJSON(w, resp)
		return
	}
	a.statusMu.RUnlock()

	p := a.statusProvider
	resp := map[string]any{
		"peer_id":          p.PeerID(),
		"mode":             p.Mode(),
		"socks5_listen":    p.Socks5Listen(),
		"p2p_listen_addrs": p.P2PListenAddrs(),
		"uptime_seconds":   int64(time.Since(p.StartTime()).Seconds()),
		"version":          update.Version,
		"traffic":          p.TrafficStats(),
	}
	if a.opts != nil && a.opts.Relays != nil {
		resp["relays_known"] = len(a.opts.Relays.ListRelays())
	}
	if a.opts != nil && a.opts.Exits != nil {
		resp["exits_known"] = len(a.opts.Exits.ListExits())
	}
	if a.opts != nil && a.opts.Pool != nil {
		if ps := a.opts.Pool.GetPoolStatus(); ps != nil {
			resp["circuit_pool"] = ps
		}
	}

	a.statusMu.Lock()
	a.statusCache = cloneStatusMap(resp)
	a.statusCacheAt = now
	a.statusMu.Unlock()
	writeJSON(w, resp)
}

func cloneStatusMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (a *LocalAPI) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.UpdateService == nil {
		http.Error(w, "update service not available", http.StatusNotFound)
		return
	}
	info, err := a.opts.UpdateService.CheckForUpdate(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, info)
}

func (a *LocalAPI) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.UpdateService == nil {
		http.Error(w, "update service not available", http.StatusNotFound)
		return
	}
	result, err := a.opts.UpdateService.ApplyUpdate(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, result)
}

func (a *LocalAPI) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.UpdateSettings == nil {
		http.Error(w, "update settings not available", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{
			"auto_update": a.opts.UpdateSettings.GetAutoUpdate(),
		})
	case http.MethodPost:
		var payload struct {
			AutoUpdate bool `json:"auto_update"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := a.opts.UpdateSettings.SetAutoUpdate(payload.AutoUpdate); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"auto_update": a.opts.UpdateSettings.GetAutoUpdate(),
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *LocalAPI) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.nodeProvider == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, a.nodeProvider.GetAll())
}

func (a *LocalAPI) handleCircuits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.circuitProvider == nil {
		writeJSON(w, []any{})
		return
	}
	// Optional: enrich circuit info with hop/stream metadata without exposing any keys.
	circuits := a.circuitProvider.ListCircuits()
	streamCounts := map[string]int{}
	if a.opts != nil && a.opts.Streams != nil {
		streams := a.opts.Streams.ListStreams()
		for _, s := range streams {
			streamCounts[s.CircuitID]++
		}
	}
	out := make([]CircuitInfoResponse, 0, len(circuits))
	for _, c := range circuits {
		hopCount := len(c.Plan.Hops)
		var relayPeerID, exitPeerID string
		if hopCount > 0 && c.Plan.Hops[0].IsRelay {
			relayPeerID = c.Plan.Hops[0].PeerID
		}
		if c.Plan.ExitHopIndex >= 0 && c.Plan.ExitHopIndex < hopCount {
			exitHop := c.Plan.Hops[c.Plan.ExitHopIndex]
			if exitHop.IsExit {
				exitPeerID = exitHop.PeerID
			}
		}
		out = append(out, CircuitInfoResponse{
			ID:                  c.ID,
			State:               c.State,
			Plan:                c.Plan,
			RelayPeerID:         relayPeerID,
			ExitPeerID:          exitPeerID,
			HopCount:            hopCount,
			StreamCount:         streamCounts[c.ID],
			CreatedAt:           c.CreatedAt,
			UpdatedAt:           c.UpdatedAt,
			BytesSent:           c.BytesSent,
			BytesReceived:       c.BytesReceived,
			LastPingAt:          c.LastPingAt,
			LastPongAt:          c.LastPongAt,
			Alive:               c.Alive,
			ConsecutiveFailures: c.ConsecutiveFailures,
			SmoothedRTTMillis:   c.SmoothedRTTMillis,
		})
	}
	writeJSON(w, out)
}

func (a *LocalAPI) handleRelays(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Relays == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, a.enrichNodesWithCountry(a.opts.Relays.ListRelays()))
}

func (a *LocalAPI) handleExits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Exits == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, a.enrichNodesWithCountry(a.opts.Exits.ListExits()))
}

// enrichNodesWithCountry returns a JSON-friendly list of node objects with a top-level "country" field for display.
func (a *LocalAPI) enrichNodesWithCountry(nodes []*discovery.NodeDescriptor) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, d := range nodes {
		var m map[string]any
		if b, err := json.Marshal(d); err == nil {
			_ = json.Unmarshal(b, &m)
		}
		if m == nil {
			m = make(map[string]any)
		}
		if a.opts != nil && a.opts.ExitCountryResolver != nil {
			m["country"] = a.opts.ExitCountryResolver.CountryForExit(d)
		} else if d.ExitInfo != nil {
			m["country"] = d.ExitInfo.Country
		} else {
			m["country"] = ""
		}
		out = append(out, m)
	}
	return out
}

func (a *LocalAPI) handleStreams(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Streams == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, a.opts.Streams.ListStreams())
}

func (a *LocalAPI) handleScores(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Scores == nil {
		writeJSON(w, map[string]any{"peers": []any{}})
		return
	}
	writeJSON(w, a.opts.Scores.GetScores())
}

func (a *LocalAPI) handleErrorsRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Errors == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, a.opts.Errors.GetRecent())
}

func (a *LocalAPI) handleMetricsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Metrics == nil {
		writeJSON(w, map[string]any{})
		return
	}
	writeJSON(w, a.opts.Metrics.GetSummary())
}

func (a *LocalAPI) handleExitSelection(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ExitSelection == nil {
		writeJSON(w, config.ExitSelectionConfig{Mode: config.ExitSelectionAuto})
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp := map[string]any{}
		if b, err := json.Marshal(a.opts.ExitSelection.GetExitSelection()); err == nil {
			_ = json.Unmarshal(b, &resp)
		}
		if a.opts.Socks5Tunnel != nil {
			resp["tunnel_to_exit"] = a.opts.Socks5Tunnel.GetTunnelToExit()
		}
		writeJSON(w, resp)
		return
	case http.MethodPost:
		var payload struct {
			config.ExitSelectionConfig
			TunnelToExit *bool `json:"tunnel_to_exit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		cfg := payload.ExitSelectionConfig
		if cfg.Mode == "" {
			cfg.Mode = config.ExitSelectionAuto
		}
		a.opts.ExitSelection.SetExitSelection(&cfg)
		if a.opts.Socks5Tunnel != nil && payload.TunnelToExit != nil {
			a.opts.Socks5Tunnel.SetTunnelToExit(*payload.TunnelToExit)
		}
		if a.opts.ConfigPath != "" {
			if err := config.SaveExitSelectionSettings(a.opts.ConfigPath, cfg, payload.TunnelToExit); err != nil {
				http.Error(w, "save to config file failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		resp := map[string]any{}
		if b, err := json.Marshal(a.opts.ExitSelection.GetExitSelection()); err == nil {
			_ = json.Unmarshal(b, &resp)
		}
		if a.opts.Socks5Tunnel != nil {
			resp["tunnel_to_exit"] = a.opts.Socks5Tunnel.GetTunnelToExit()
		}
		writeJSON(w, resp)
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

func (a *LocalAPI) handleExitCandidates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.ExitCandidates == nil {
		writeJSON(w, []any{})
		return
	}
	list, err := a.opts.ExitCandidates.ListExitCandidates()
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "candidates": []any{}})
		return
	}
	type candidate struct {
		PeerID  string `json:"peer_id"`
		Country string `json:"country,omitempty"`
		Version string `json:"version,omitempty"`
	}
	out := make([]candidate, 0, len(list))
	for _, n := range list {
		c := candidate{PeerID: n.PeerID, Version: n.Version}
		if a.opts != nil && a.opts.ExitCountryResolver != nil {
			c.Country = a.opts.ExitCountryResolver.CountryForExit(n)
		} else if n.ExitInfo != nil {
			c.Country = n.ExitInfo.Country
		}
		out = append(out, c)
	}
	writeJSON(w, map[string]any{"candidates": out, "count": len(out)})
}

func (a *LocalAPI) handleCircuitPoolConfig(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.CircuitPoolConfig == nil {
		http.Error(w, "circuit pool config not available", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		cfg := a.opts.CircuitPoolConfig.GetPoolConfig()
		if cfg == nil {
			http.Error(w, "circuit pool config not available", http.StatusNotFound)
			return
		}
		writeJSON(w, cfg)
		return
	case http.MethodPost:
		var body struct {
			MinTotal int `json:"min_total"`
			MaxTotal int `json:"max_total"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json body", http.StatusBadRequest)
			return
		}
		if body.MinTotal <= 0 || body.MaxTotal <= 0 {
			http.Error(w, "min_total and max_total must be positive", http.StatusBadRequest)
			return
		}
		if body.MaxTotal < body.MinTotal {
			http.Error(w, "max_total must be >= min_total", http.StatusBadRequest)
			return
		}
		if !a.opts.CircuitPoolConfig.SetPoolTotalLimits(body.MinTotal, body.MaxTotal) {
			http.Error(w, "circuit pool config not available", http.StatusNotFound)
			return
		}
		cfg := a.opts.CircuitPoolConfig.GetPoolConfig()
		if cfg == nil {
			http.Error(w, "circuit pool config not available", http.StatusNotFound)
			return
		}
		if a.opts.ConfigPath != "" {
			if err := config.SaveCircuitPoolConfig(a.opts.ConfigPath, *cfg); err != nil {
				http.Error(w, "save circuit pool config failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		writeJSON(w, cfg)
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

// ExitStatusResponse 出口運行狀態（drain、連接數、最近拒絕）。
type ExitStatusResponse struct {
	DrainMode        bool               `json:"drain_mode"`
	AcceptNewStreams bool               `json:"accept_new_streams"`
	OpenConnections  int                `json:"open_connections"`
	RecentRejects    []exit.RejectEntry `json:"recent_rejects"`
}

func (a *LocalAPI) handleExitPolicy(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ExitService == nil || a.opts.ExitService.Policy == nil {
		http.Error(w, "exit policy not available", http.StatusNotFound)
		return
	}
	svc := a.opts.ExitService
	p := svc.Policy
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"policy": p.GetPolicy(), "runtime": p.GetRuntime()})
		return
	case http.MethodPost:
		var body struct {
			Policy  *config.ExitPolicyConfig  `json:"policy"`
			Runtime *config.ExitRuntimeConfig `json:"runtime"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.Policy != nil {
			p.SetPolicy(*body.Policy)
		}
		if body.Runtime != nil {
			p.SetRuntime(*body.Runtime)
		}
		if a.opts.ConfigPath != "" {
			exitCfg := config.ExitConfig{
				Enabled: true,
				Policy:  p.GetPolicy(),
				Runtime: p.GetRuntime(),
			}
			if err := config.SaveExitConfig(a.opts.ConfigPath, exitCfg); err != nil {
				http.Error(w, "save exit config failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		writeJSON(w, map[string]any{"policy": p.GetPolicy(), "runtime": p.GetRuntime()})
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

func (a *LocalAPI) handleExitStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.ExitService == nil || a.opts.ExitService.Policy == nil {
		http.Error(w, "exit status not available", http.StatusNotFound)
		return
	}
	svc := a.opts.ExitService
	p := svc.Policy
	writeJSON(w, ExitStatusResponse{
		DrainMode:        p.DrainMode(),
		AcceptNewStreams: p.AcceptNewStreams(),
		OpenConnections:  svc.OpenConnCount(),
		RecentRejects:    svc.GetRecentRejects(),
	})
}

func (a *LocalAPI) handleExitDrain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.ExitService == nil || a.opts.ExitService.Policy == nil {
		http.Error(w, "exit not available", http.StatusNotFound)
		return
	}
	p := a.opts.ExitService.Policy
	p.SetRuntime(config.ExitRuntimeConfig{DrainMode: true, AcceptNewStreams: false})
	if a.opts.ConfigPath != "" {
		exitCfg := config.ExitConfig{Enabled: true, Policy: p.GetPolicy(), Runtime: p.GetRuntime()}
		if err := config.SaveExitConfig(a.opts.ConfigPath, exitCfg); err != nil {
			http.Error(w, "save exit config failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, map[string]string{"status": "drain"})
}

func (a *LocalAPI) handleExitResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.ExitService == nil || a.opts.ExitService.Policy == nil {
		http.Error(w, "exit not available", http.StatusNotFound)
		return
	}
	p := a.opts.ExitService.Policy
	p.SetRuntime(config.ExitRuntimeConfig{DrainMode: false, AcceptNewStreams: true})
	if a.opts.ConfigPath != "" {
		exitCfg := config.ExitConfig{Enabled: true, Policy: p.GetPolicy(), Runtime: p.GetRuntime()}
		if err := config.SaveExitConfig(a.opts.ConfigPath, exitCfg); err != nil {
			http.Error(w, "save exit config failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, map[string]string{"status": "resume"})
}

func (a *LocalAPI) handleChatMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	profile, err := a.opts.ChatService.GetProfile()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, profile)
}

func (a *LocalAPI) handleIdentityPrivateKeyExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Identity == nil {
		http.Error(w, "identity service not available", http.StatusNotFound)
		return
	}
	privateKeyBase58, peerID, err := a.opts.Identity.ExportIdentityPrivateKeyBase58()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"encoding":           "base58",
		"peer_id":            peerID,
		"private_key_base58": privateKeyBase58,
		"requires_restart":   false,
	})
}

func (a *LocalAPI) handleIdentityPrivateKeyImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Identity == nil {
		http.Error(w, "identity service not available", http.StatusNotFound)
		return
	}
	var body struct {
		PrivateKeyBase58 string `json:"private_key_base58"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	peerID, err := a.opts.Identity.ImportIdentityPrivateKeyBase58(strings.TrimSpace(body.PrivateKeyBase58))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"ok":               true,
		"encoding":         "base58",
		"peer_id":          peerID,
		"requires_restart": true,
	})
}

func (a *LocalAPI) handleIdentityChallengeSign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Identity == nil {
		http.Error(w, "identity service not available", http.StatusNotFound)
		return
	}
	var body struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Challenge == "" {
		http.Error(w, "challenge is empty", http.StatusBadRequest)
		return
	}
	signatureBase64, publicKeyBase64, peerID, err := a.opts.Identity.SignChallenge(body.Challenge)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"peer_id":             peerID,
		"challenge":           body.Challenge,
		"signature":           signatureBase64,
		"public_key":          publicKeyBase64,
		"signature_base64":    signatureBase64,
		"public_key_base64":   publicKeyBase64,
		"signature_encoding":  "base64",
		"public_key_encoding": "base64",
	})
}

func (a *LocalAPI) handleChatProfile(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		profile, err := a.opts.ChatService.GetProfile()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, profile)
	case http.MethodPost:
		var body struct {
			Nickname string `json:"nickname"`
			Bio      string `json:"bio"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		profile, err := a.opts.ChatService.UpdateProfile(body.Nickname, body.Bio)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, profile)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *LocalAPI) handleChatProfileAvatar(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(chat.MaxProfileAvatarBytes + (1 << 20)); err != nil {
		http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("avatar")
	if err != nil {
		http.Error(w, "missing avatar: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, chat.MaxProfileAvatarBytes+1))
	if err != nil {
		http.Error(w, "read avatar failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(data) == 0 {
		http.Error(w, "avatar is empty", http.StatusBadRequest)
		return
	}
	if len(data) > chat.MaxProfileAvatarBytes {
		http.Error(w, "avatar too large: max 512KB", http.StatusBadRequest)
		return
	}
	profile, err := a.opts.ChatService.UpdateProfileAvatar(header.Filename, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, profile)
}

func (a *LocalAPI) handleChatProfileBindingDraft(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		EthAddress string `json:"eth_address"`
		ChainID    uint64 `json:"chain_id"`
		TTLSeconds int64  `json:"ttl_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	draft, err := a.opts.ChatService.CreateBindingDraft(body.EthAddress, body.ChainID, body.TTLSeconds)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, draft)
}

func (a *LocalAPI) handleChatProfileBinding(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Payload       binding.BindingPayload `json:"payload"`
			PeerSignature string                 `json:"peer_signature"`
			EthSignature  string                 `json:"eth_signature"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		profile, err := a.opts.ChatService.FinalizeBindingRecord(body.Payload, body.PeerSignature, body.EthSignature)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, profile)
	case http.MethodDelete:
		if _, err := a.opts.ChatService.ClearLocalBinding(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *LocalAPI) handleChatAvatar(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/chat/avatars/")
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == string(filepath.Separator) {
		http.NotFound(w, r)
		return
	}
	path, err := a.opts.ChatService.AvatarPath(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}

func (a *LocalAPI) handleChatRequests(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		reqs, err := a.opts.ChatService.ListRequests()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, reqs)
	case http.MethodPost:
		var body struct {
			ToPeerID  string `json:"to_peer_id"`
			IntroText string `json:"intro_text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		req, err := a.opts.ChatService.SendRequest(body.ToPeerID, body.IntroText)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, req)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *LocalAPI) handleChatContacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	contacts, err := a.opts.ChatService.ListContacts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, contacts)
}

func (a *LocalAPI) handleChatContactItem(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/chat/contacts/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := a.opts.ChatService.DeleteContactLocal(parts[0]); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "peer_id": parts[0]})
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	peerID, action := parts[0], parts[1]
	if action == "binding" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		details, err := a.opts.ChatService.GetContactBindingDetails(peerID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, details)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch action {
	case "nickname":
		var body struct {
			Nickname string `json:"nickname"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		contact, err := a.opts.ChatService.UpdateContactNickname(peerID, body.Nickname)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, contact)
	case "block":
		var body struct {
			Blocked bool `json:"blocked"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		contact, err := a.opts.ChatService.SetContactBlocked(peerID, body.Blocked)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, contact)
	default:
		http.NotFound(w, r)
	}
}

func (a *LocalAPI) handleChatConversations(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		convs, err := a.opts.ChatService.ListConversations()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, convs)
	case http.MethodPost:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *LocalAPI) handleChatRequestItem(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/chat/requests/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	requestID, action := parts[0], parts[1]
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch action {
	case "accept":
		conv, err := a.opts.ChatService.AcceptRequest(requestID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, conv)
	case "reject":
		if err := a.opts.ChatService.RejectRequest(requestID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "rejected"})
	default:
		http.NotFound(w, r)
	}
}

func (a *LocalAPI) handleChatConversationItem(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/chat/conversations/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := a.opts.ChatService.DeleteConversationLocal(parts[0]); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "conversation_id": parts[0]})
		return
	}
	conversationID, action := parts[0], parts[1]
	switch action {
	case "sync":
		if len(parts) != 2 || r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Sync involves peer dial + stream/Relay (tens of seconds). Run in background
		// so the HTTP client returns immediately; poll GET .../messages for updates.
		cid := conversationID
		safe.Go("chat.api.syncConversation", func() {
			if err := a.opts.ChatService.SyncConversation(cid); err != nil {
				log.Printf("[api] chat sync failed conversation=%s: %v", cid, err)
			}
		})
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]any{
			"conversation_id": conversationID,
			"status":          "sync_requested",
		})
		return
	case "read":
		if len(parts) != 2 || r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		conv, err := a.opts.ChatService.ClearConversationUnreadCount(conversationID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, conv)
		return
	case "messages":
		if len(parts) == 4 && parts[3] == "revoke" {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			msgID := parts[2]
			if err := a.opts.ChatService.RevokeMessage(conversationID, msgID); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]string{"status": "revoked"})
			return
		}
		if len(parts) == 4 && parts[3] == "file" {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			msgID := parts[2]
			msg, blob, err := a.opts.ChatService.GetMessageFile(conversationID, msgID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mimeType := msg.MIMEType
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			w.Header().Set("Content-Type", mimeType)
			w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(msg.FileName)+`"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(blob)
			return
		}
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			q := r.URL.Query()
			_, hasLimit := q["limit"]
			_, hasOffset := q["offset"]
			if !hasLimit && !hasOffset {
				msgs, err := a.opts.ChatService.ListMessages(conversationID)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, msgs)
			} else {
				limit := 100
				if hasLimit {
					parsed, err := strconv.Atoi(strings.TrimSpace(q.Get("limit")))
					if err != nil || parsed < 1 || parsed > 500 {
						http.Error(w, "limit must be between 1 and 500", http.StatusBadRequest)
						return
					}
					limit = parsed
				}
				offset := 0
				if hasOffset {
					parsed, err := strconv.Atoi(strings.TrimSpace(q.Get("offset")))
					if err != nil || parsed < 0 {
						http.Error(w, "offset must be a non-negative integer", http.StatusBadRequest)
						return
					}
					offset = parsed
				}
				msgs, total, err := a.opts.ChatService.ListMessagesPage(conversationID, limit, offset)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				hasMore := offset+len(msgs) < total
				writeJSON(w, map[string]any{
					"messages": msgs,
					"total":    total,
					"limit":    limit,
					"offset":   offset,
					"has_more": hasMore,
				})
			}
		case http.MethodPost:
			var body struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
			msg, err := a.opts.ChatService.SendText(conversationID, body.Text)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, msg)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case "files":
		switch r.Method {
		case http.MethodPost:
			if err := r.ParseMultipartForm(chat.MaxChatFileBytes + (1 << 20)); err != nil {
				http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
				return
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				http.Error(w, "missing file: "+err.Error(), http.StatusBadRequest)
				return
			}
			defer file.Close()
			data, err := chat.ValidateChatFileData(file)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			fileName := chat.NormalizeChatFileName(header.Filename)
			mimeType := http.DetectContentType(data)
			uploadToIPFS := false
			if v := strings.TrimSpace(r.FormValue("upload_to_ipfs")); v != "" {
				uploadToIPFS, _ = strconv.ParseBool(v)
			}
			msg, err := a.opts.ChatService.SendFile(conversationID, fileName, mimeType, data, uploadToIPFS)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, msg)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case "retention":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			RetentionMinutes int `json:"retention_minutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		conv, err := a.opts.ChatService.UpdateConversationRetention(conversationID, body.RetentionMinutes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, conv)
	default:
		http.NotFound(w, r)
	}
}

func (a *LocalAPI) handleGroups(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		groups, err := a.opts.ChatService.ListGroups()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, groups)
	case http.MethodPost:
		var body struct {
			Title   string   `json:"title"`
			Members []string `json:"members"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		group, err := a.opts.ChatService.CreateGroup(body.Title, body.Members)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, group)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *LocalAPI) handleGroupItem(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/groups/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	groupID := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		details, err := a.opts.ChatService.GetGroupDetails(groupID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, details)
		return
	}
	action := parts[1]
	switch action {
	case "invite":
		var body struct {
			PeerID     string `json:"peer_id"`
			Role       string `json:"role"`
			InviteText string `json:"invite_text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		group, err := a.opts.ChatService.InviteGroupMember(groupID, body.PeerID, body.Role, body.InviteText)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, group)
	case "join":
		group, err := a.opts.ChatService.AcceptGroupInvite(groupID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, group)
	case "leave":
		var body struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		group, err := a.opts.ChatService.LeaveGroup(groupID, body.Reason)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, group)
	case "remove":
		var body struct {
			PeerID string `json:"peer_id"`
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		group, err := a.opts.ChatService.RemoveGroupMember(groupID, body.PeerID, body.Reason)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, group)
	case "title":
		var body struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		group, err := a.opts.ChatService.UpdateGroupTitle(groupID, body.Title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, group)
	case "retention":
		var body struct {
			RetentionMinutes int `json:"retention_minutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		group, err := a.opts.ChatService.UpdateGroupRetention(groupID, body.RetentionMinutes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, group)
	case "dissolve":
		var body struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		group, err := a.opts.ChatService.DissolveGroup(groupID, body.Reason)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, group)
	case "controller":
		var body struct {
			PeerID string `json:"peer_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		group, err := a.opts.ChatService.TransferGroupController(groupID, body.PeerID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, group)
	case "messages":
		if len(parts) == 4 && parts[3] == "revoke" {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			msgID := parts[2]
			if err := a.opts.ChatService.RevokeGroupMessage(groupID, msgID); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]any{"ok": true})
			return
		}
		if len(parts) == 4 && parts[3] == "file" {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			msgID := parts[2]
			msg, blob, err := a.opts.ChatService.GetGroupMessageFile(groupID, msgID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mimeType := msg.MIMEType
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			w.Header().Set("Content-Type", mimeType)
			w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(msg.FileName)+`"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(blob)
			return
		}
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			msgs, err := a.opts.ChatService.ListGroupMessages(groupID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, msgs)
		case http.MethodPost:
			var body struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
			msg, err := a.opts.ChatService.SendGroupText(groupID, body.Text)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, msg)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case "files":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseMultipartForm(chat.MaxChatFileBytes + (1 << 20)); err != nil {
			http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing file: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		data, err := chat.ValidateChatFileData(file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fileName := chat.NormalizeChatFileName(header.Filename)
		mimeType := http.DetectContentType(data)
		msg, err := a.opts.ChatService.SendGroupFile(groupID, fileName, mimeType, data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, msg)
	case "sync":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			FromPeerID string `json:"from_peer_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := a.opts.ChatService.SyncGroup(groupID, body.FromPeerID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"ok":           true,
			"group_id":     groupID,
			"from_peer_id": body.FromPeerID,
		})
	default:
		http.NotFound(w, r)
	}
}

type publicChannelSummaryResponse struct {
	ChannelID string `json:"channel_id"`
	publicchannel.ChannelSummary
}

func newPublicChannelSummaryResponse(summary publicchannel.ChannelSummary) publicChannelSummaryResponse {
	return publicChannelSummaryResponse{
		ChannelID:      summary.Profile.ChannelID,
		ChannelSummary: summary,
	}
}

func (a *LocalAPI) triggerPublicChannelMessageLoad(channelID string, beforeMessageID int64, limit int) {
	if a.opts == nil || a.opts.PublicChannels == nil {
		return
	}
	safe.Go("publicchannel.api.loadMessages", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := a.opts.PublicChannels.LoadMessagesFromProviders(ctx, channelID, beforeMessageID, limit); err != nil {
			log.Printf("[publicchannel] async load messages %s before=%d limit=%d: %v", channelID, beforeMessageID, limit, err)
		}
	})
}

func (a *LocalAPI) triggerPublicChannelSync(channelID string) {
	if a.opts == nil || a.opts.PublicChannels == nil {
		return
	}
	safe.Go("publicchannel.api.sync", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := a.opts.PublicChannels.SyncChannel(ctx, channelID); err != nil {
			log.Printf("[publicchannel] async sync channel %s: %v", channelID, err)
		}
	})
}

func (a *LocalAPI) handlePublicChannels(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.PublicChannels == nil {
		http.Error(w, "public channel service not available", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		ownerPeerID := strings.TrimSpace(r.URL.Query().Get("owner_peer_id"))
		if ownerPeerID == "" {
			ownerPeerID = strings.TrimSpace(r.URL.Query().Get("owner"))
		}
		items, err := a.opts.PublicChannels.ListChannelsByOwner(ownerPeerID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, items)
	case http.MethodPost:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "multipart/form-data") {
			if err := r.ParseMultipartForm(chat.MaxProfileAvatarBytes + (1 << 20)); err != nil {
				http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
				return
			}
			file, header, err := r.FormFile("avatar")
			if err != nil {
				http.Error(w, "missing avatar: "+err.Error(), http.StatusBadRequest)
				return
			}
			defer file.Close()
			data, err := chat.ValidateChatFileData(file)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mimeType := strings.TrimSpace(r.FormValue("mime_type"))
			if mimeType == "" {
				mimeType = http.DetectContentType(data)
			}
			retentionMinutes := 0
			if raw := strings.TrimSpace(r.FormValue("message_retention_minutes")); raw != "" {
				parsed, err := strconv.Atoi(raw)
				if err != nil {
					http.Error(w, "invalid message_retention_minutes: "+err.Error(), http.StatusBadRequest)
					return
				}
				retentionMinutes = parsed
			}
			item, err := a.opts.PublicChannels.CreateChannelWithAvatar(
				r.FormValue("name"),
				r.FormValue("bio"),
				retentionMinutes,
				header.Filename,
				mimeType,
				data,
			)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, newPublicChannelSummaryResponse(item))
			return
		}
		var body publicchannel.CreateChannelInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		item, err := a.opts.PublicChannels.CreateChannel(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, newPublicChannelSummaryResponse(item))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *LocalAPI) handlePublicChannelSubscriptions(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.PublicChannels == nil {
		http.Error(w, "public channel service not available", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := a.opts.PublicChannels.ListSubscribedChannels()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if items == nil {
		items = []publicchannel.ChannelSummary{}
	}
	writeJSON(w, items)
}

func (a *LocalAPI) handlePublicChannelItem(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.PublicChannels == nil {
		http.Error(w, "public channel service not available", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/public-channels/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	channelID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			item, err := a.opts.PublicChannels.GetChannelSummary(channelID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, item)
		case http.MethodPut:
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "multipart/form-data") {
				if err := r.ParseMultipartForm(chat.MaxProfileAvatarBytes + (1 << 20)); err != nil {
					http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
					return
				}
				file, header, err := r.FormFile("avatar")
				if err != nil {
					http.Error(w, "missing avatar: "+err.Error(), http.StatusBadRequest)
					return
				}
				defer file.Close()
				data, err := chat.ValidateChatFileData(file)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				mimeType := strings.TrimSpace(r.FormValue("mime_type"))
				if mimeType == "" {
					mimeType = http.DetectContentType(data)
				}
				retentionMinutes := 0
				if raw := strings.TrimSpace(r.FormValue("message_retention_minutes")); raw != "" {
					parsed, err := strconv.Atoi(raw)
					if err != nil {
						http.Error(w, "invalid message_retention_minutes: "+err.Error(), http.StatusBadRequest)
						return
					}
					retentionMinutes = parsed
				} else {
					current, err := a.opts.PublicChannels.GetChannelSummary(channelID)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					retentionMinutes = current.Profile.MessageRetentionMinutes
				}
				item, err := a.opts.PublicChannels.UpdateChannelProfileWithAvatar(
					channelID,
					r.FormValue("name"),
					r.FormValue("bio"),
					retentionMinutes,
					header.Filename,
					mimeType,
					data,
				)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, newPublicChannelSummaryResponse(item))
				return
			}
			var body publicchannel.UpdateChannelProfileInput
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
			item, err := a.opts.PublicChannels.UpdateChannelProfile(channelID, body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, newPublicChannelSummaryResponse(item))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	switch parts[1] {
	case "head":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		item, err := a.opts.PublicChannels.GetChannelHead(channelID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, item)
	case "subscribe":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			PeerID      string   `json:"peer_id"`
			PeerIDs     []string `json:"peer_ids"`
			LastSeenSeq int64    `json:"last_seen_seq"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.PeerID) != "" {
			body.PeerIDs = append(body.PeerIDs, strings.TrimSpace(body.PeerID))
		}
		item, err := a.opts.PublicChannels.SubscribeChannelAsync(channelID, body.PeerIDs, body.LastSeenSeq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, item)
	case "read":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		item, err := a.opts.PublicChannels.ClearChannelUnreadCount(channelID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, item)
	case "unsubscribe":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := a.opts.PublicChannels.UnsubscribeChannel(channelID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "channel_id": channelID})
	case "sync":
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		afterSeq, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("after_seq")), 10, 64)
		limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
		if r.Method == http.MethodPost {
			a.triggerPublicChannelSync(channelID)
			writeJSON(w, map[string]any{
				"ok":         true,
				"channel_id": channelID,
				"scheduled":  true,
			})
			return
		}
		item, err := a.opts.PublicChannels.GetChannelChanges(channelID, afterSeq, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, item)
	case "providers":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		items, err := a.opts.PublicChannels.ListProviders(channelID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, items)
	case "messages":
		if len(parts) == 2 {
			switch r.Method {
			case http.MethodGet:
				beforeMessageID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("before_message_id")), 10, 64)
				limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
				if limit <= 0 {
					limit = publicchannel.DefaultPageLimit
				}
				items, err := a.opts.PublicChannels.GetChannelMessages(channelID, beforeMessageID, limit)
				if err == sql.ErrNoRows {
					items = nil
					err = nil
				}
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				needAsyncLoad := false
				if beforeMessageID > 0 {
					needAsyncLoad = len(items) < limit
				} else if len(items) == 0 {
					needAsyncLoad = true
				}
				if needAsyncLoad {
					a.triggerPublicChannelMessageLoad(channelID, beforeMessageID, limit)
				}
				if items == nil {
					items = []publicchannel.ChannelMessage{}
				}
				writeJSON(w, map[string]any{"channel_id": channelID, "items": items})
			case http.MethodPost:
				var body publicchannel.UpsertMessageInput
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
					return
				}
				item, err := a.opts.PublicChannels.CreateChannelMessage(channelID, body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, item)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if len(parts) == 3 && parts[2] == "files" {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := r.ParseMultipartForm(chat.MaxChatFileBytes + (1 << 20)); err != nil {
				http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
				return
			}
			fileHeaders := r.MultipartForm.File["files"]
			if len(fileHeaders) == 0 {
				http.Error(w, "missing files", http.StatusBadRequest)
				return
			}
			uploads := make([]publicchannel.UploadFileInput, 0, len(fileHeaders))
			for _, header := range fileHeaders {
				file, err := header.Open()
				if err != nil {
					http.Error(w, "open file: "+err.Error(), http.StatusBadRequest)
					return
				}
				data, err := chat.ValidateChatFileData(file)
				_ = file.Close()
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				mimeType := strings.TrimSpace(header.Header.Get("Content-Type"))
				if mimeType == "" {
					mimeType = http.DetectContentType(data)
				}
				uploads = append(uploads, publicchannel.UploadFileInput{
					FileName: header.Filename,
					MIMEType: mimeType,
					Data:     data,
				})
			}
			item, err := a.opts.PublicChannels.CreateChannelFilesMessage(channelID, r.FormValue("text"), uploads)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, item)
			return
		}
		if len(parts) == 3 && parts[2] == "file" {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := r.ParseMultipartForm(chat.MaxChatFileBytes + (1 << 20)); err != nil {
				http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
				return
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				http.Error(w, "missing file: "+err.Error(), http.StatusBadRequest)
				return
			}
			defer file.Close()
			data, err := chat.ValidateChatFileData(file)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			fileName := chat.NormalizeChatFileName(header.Filename)
			mimeType := strings.TrimSpace(r.FormValue("mime_type"))
			if mimeType == "" {
				mimeType = http.DetectContentType(data)
			}
			text := r.FormValue("text")
			item, err := a.opts.PublicChannels.CreateChannelFileMessage(channelID, text, fileName, mimeType, data)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, item)
			return
		}
		if len(parts) == 3 {
			messageID, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil {
				http.Error(w, "invalid message_id", http.StatusBadRequest)
				return
			}
			switch r.Method {
			case http.MethodGet:
				item, err := a.opts.PublicChannels.GetChannelMessage(channelID, messageID)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, item)
			case http.MethodPut:
				var body publicchannel.UpsertMessageInput
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
					return
				}
				item, err := a.opts.PublicChannels.UpdateChannelMessage(r.Context(), channelID, messageID, body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, item)
			case http.MethodDelete:
				item, err := a.opts.PublicChannels.DeleteChannelMessage(r.Context(), channelID, messageID)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, item)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (a *LocalAPI) handleMeshServerServers(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.MeshServer == nil {
		http.Error(w, "meshserver client not available", http.StatusNotFound)
		return
	}
	connection := strings.TrimSpace(r.URL.Query().Get("connection"))

	switch r.Method {
	case http.MethodGet:
		resp, err := a.opts.MeshServer.ListServers(r.Context(), connection)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Convert server terminology to Space terminology for the response.
		type spaceSummary struct {
			ID                   int                  `json:"id"`
			SpaceID              uint32               `json:"space_id"`
			Name                 string               `json:"name"`
			AvatarUrl            string               `json:"avatar_url"`
			Description          string               `json:"description"`
			Visibility           sessionv1.Visibility `json:"visibility"`
			MemberCount          uint32               `json:"member_count"`
			AllowChannelCreation bool                 `json:"allow_channel_creation"`
		}
		type listSpacesResp struct {
			Spaces []*spaceSummary `json:"spaces"`
		}

		out := &listSpacesResp{
			Spaces: make([]*spaceSummary, 0, len(resp.Spaces)),
		}
		for _, s := range resp.Spaces {
			if s == nil {
				continue
			}
			spaceIntID := a.meshSpaceIntID(s.SpaceId)
			out.Spaces = append(out.Spaces, &spaceSummary{
				ID:                   spaceIntID,
				SpaceID:              s.SpaceId,
				Name:                 s.Name,
				AvatarUrl:            s.AvatarUrl,
				Description:          s.Description,
				Visibility:           s.Visibility,
				MemberCount:          s.MemberCount,
				AllowChannelCreation: s.AllowChannelCreation,
			})
		}
		writeJSON(w, out)
		return

	case http.MethodPost:
		var body struct {
			Name                 string `json:"name"`
			Description          string `json:"description"`
			Visibility           string `json:"visibility"`
			AllowChannelCreation bool   `json:"allow_channel_creation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		visibility := parseMeshServerVisibility(body.Visibility)
		resp, err := a.opts.MeshServer.CreateSpace(r.Context(), connection, body.Name, body.Description, visibility, body.AllowChannelCreation)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := map[string]any{
			"ok":       resp.Ok,
			"space_id": resp.SpaceId,
			"message":  resp.Message,
		}
		if resp.Space != nil {
			s := resp.Space
			out["space"] = map[string]any{
				"id":                     a.meshSpaceIntID(s.SpaceId),
				"space_id":               s.SpaceId,
				"name":                   s.Name,
				"avatar_url":             s.AvatarUrl,
				"description":            s.Description,
				"visibility":             s.Visibility,
				"member_count":           s.MemberCount,
				"allow_channel_creation": s.AllowChannelCreation,
			}
		}
		writeJSON(w, out)
		return

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

func (a *LocalAPI) handleMeshServerConnectedServers(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.MeshServer == nil {
		http.Error(w, "meshserver client not available", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Persistent "connected meshservers" list from local JSON DB.
	writeJSON(w, map[string]any{"servers": a.opts.MeshServer.ListConnectedServers()})
}

func (a *LocalAPI) handleMeshServerMediaItem(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.MeshServer == nil {
		http.Error(w, "meshserver client not available", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mediaID := strings.TrimPrefix(r.URL.Path, "/api/v1/meshserver/media/")
	mediaID = strings.Trim(strings.TrimSpace(mediaID), "/")
	if mediaID == "" {
		http.NotFound(w, r)
		return
	}

	connection := strings.TrimSpace(r.URL.Query().Get("connection"))
	file, err := a.opts.MeshServer.GetMedia(r.Context(), connection, mediaID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if file == nil || len(file.InlineData) == 0 {
		http.Error(w, "media inline_data is empty", http.StatusBadRequest)
		return
	}

	mimeType := strings.TrimSpace(file.MimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)
	// Best-effort attachment name.
	if strings.TrimSpace(file.FileName) != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(file.FileName)+`"`)
	} else {
		w.Header().Set("Content-Disposition", `attachment; filename="`+mediaID+`"`)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.InlineData)
}

func (a *LocalAPI) handleMeshServerMyServers(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.MeshServer == nil {
		http.Error(w, "meshserver client not available", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := a.opts.MeshServer.ListMyServers(r.Context(), strings.TrimSpace(r.URL.Query().Get("connection")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Convert server terminology to Space terminology for nested response fields.
	type spaceSummary struct {
		ID                   int                  `json:"id"`
		SpaceID              uint32               `json:"space_id"`
		Name                 string               `json:"name"`
		AvatarUrl            string               `json:"avatar_url"`
		Description          string               `json:"description"`
		Visibility           sessionv1.Visibility `json:"visibility"`
		MemberCount          uint32               `json:"member_count"`
		AllowChannelCreation bool                 `json:"allow_channel_creation"`
	}
	type mySpaceItem struct {
		Space *spaceSummary        `json:"space"`
		Role  sessionv1.MemberRole `json:"role"`
	}
	type listMySpacesResp struct {
		Servers []*mySpaceItem `json:"servers"`
	}

	if resp == nil {
		writeJSON(w, &listMySpacesResp{Servers: []*mySpaceItem{}})
		return
	}

	out := &listMySpacesResp{Servers: make([]*mySpaceItem, 0, len(resp.Servers))}
	for _, it := range resp.Servers {
		if it == nil || it.Server == nil {
			continue
		}
		spaceIntID := a.meshSpaceIntID(it.Server.SpaceId)
		out.Servers = append(out.Servers, &mySpaceItem{
			Space: &spaceSummary{
				ID:                   spaceIntID,
				SpaceID:              it.Server.SpaceId,
				Name:                 it.Server.Name,
				AvatarUrl:            it.Server.AvatarUrl,
				Description:          it.Server.Description,
				Visibility:           it.Server.Visibility,
				MemberCount:          it.Server.MemberCount,
				AllowChannelCreation: it.Server.AllowChannelCreation,
			},
			Role: it.Role,
		})
	}
	writeJSON(w, out)
}

func (a *LocalAPI) handleMeshServerServerItem(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.MeshServer == nil {
		http.Error(w, "meshserver client not available", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/meshserver/spaces/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	serverID := parts[0]
	action := parts[1]
	connection := strings.TrimSpace(r.URL.Query().Get("connection"))
	switch action {
	case "channels":
		if len(parts) == 2 {
			switch r.Method {
			case http.MethodGet:
				resp, err := a.opts.MeshServer.ListChannels(r.Context(), connection, serverID)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				type channelSummaryWithID struct {
					ID              int                   `json:"id"`
					ChannelId       uint32                `json:"channel_id"`
					ServerId        uint32                `json:"server_id"`
					Type            sessionv1.ChannelType `json:"type"`
					Name            string                `json:"name"`
					Description     string                `json:"description"`
					Visibility      sessionv1.Visibility  `json:"visibility"`
					SlowModeSeconds uint32                `json:"slow_mode_seconds"`
					LastSeq         uint64                `json:"last_seq"`
					CanView         bool                  `json:"can_view"`
					CanSendMessage  bool                  `json:"can_send_message"`
					CanSendImage    bool                  `json:"can_send_image"`
					CanSendFile     bool                  `json:"can_send_file"`
				}
				type listChannelsRespWithID struct {
					ServerId string                  `json:"server_id"`
					Channels []*channelSummaryWithID `json:"channels"`
				}

				out := &listChannelsRespWithID{
					ServerId: serverID,
					Channels: make([]*channelSummaryWithID, 0),
				}
				if resp != nil {
					out.Channels = make([]*channelSummaryWithID, 0, len(resp.Channels))
					for _, ch := range resp.Channels {
						if ch == nil {
							continue
						}
						chIntID := a.meshChannelIntID(ch.ChannelId)
						out.Channels = append(out.Channels, &channelSummaryWithID{
							ID:              chIntID,
							ChannelId:       ch.ChannelId,
							ServerId:        ch.SpaceId,
							Type:            ch.Type,
							Name:            ch.Name,
							Description:     ch.Description,
							Visibility:      ch.Visibility,
							SlowModeSeconds: ch.SlowModeSeconds,
							LastSeq:         ch.LastSeq,
							CanView:         ch.CanView,
							CanSendMessage:  ch.CanSendMessage,
							CanSendImage:    ch.CanSendImage,
							CanSendFile:     ch.CanSendFile,
						})
					}
				}
				writeJSON(w, out)
				return
			case http.MethodPost:
				var body struct {
					Name            string `json:"name"`
					Description     string `json:"description"`
					Visibility      string `json:"visibility"`
					SlowModeSeconds uint32 `json:"slow_mode_seconds"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
					return
				}
				resp, err := a.opts.MeshServer.CreateChannel(r.Context(), connection, serverID, body.Name, body.Description, parseMeshServerVisibility(body.Visibility), body.SlowModeSeconds)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				type channelSummaryWithID struct {
					ID              int                   `json:"id"`
					ChannelId       uint32                `json:"channel_id"`
					ServerId        uint32                `json:"server_id"`
					Type            sessionv1.ChannelType `json:"type"`
					Name            string                `json:"name"`
					Description     string                `json:"description"`
					Visibility      sessionv1.Visibility  `json:"visibility"`
					SlowModeSeconds uint32                `json:"slow_mode_seconds"`
					LastSeq         uint64                `json:"last_seq"`
					CanView         bool                  `json:"can_view"`
					CanSendMessage  bool                  `json:"can_send_message"`
					CanSendImage    bool                  `json:"can_send_image"`
					CanSendFile     bool                  `json:"can_send_file"`
				}
				type createChannelRespWithID struct {
					Ok        bool                  `json:"ok"`
					ServerID  uint32                `json:"server_id"`
					ChannelID uint32                `json:"channel_id"`
					Channel   *channelSummaryWithID `json:"channel"`
					Message   string                `json:"message"`
				}
				out := &createChannelRespWithID{
					Ok:        resp.Ok,
					ServerID:  resp.SpaceId,
					ChannelID: resp.ChannelId,
					Message:   resp.Message,
				}
				if resp.Channel != nil {
					chID := a.meshChannelIntID(resp.Channel.ChannelId)
					out.Channel = &channelSummaryWithID{
						ID:              chID,
						ChannelId:       resp.Channel.ChannelId,
						ServerId:        resp.Channel.SpaceId,
						Type:            resp.Channel.Type,
						Name:            resp.Channel.Name,
						Description:     resp.Channel.Description,
						Visibility:      resp.Channel.Visibility,
						SlowModeSeconds: resp.Channel.SlowModeSeconds,
						LastSeq:         resp.Channel.LastSeq,
						CanView:         resp.Channel.CanView,
						CanSendMessage:  resp.Channel.CanSendMessage,
						CanSendImage:    resp.Channel.CanSendImage,
						CanSendFile:     resp.Channel.CanSendFile,
					}
				}
				writeJSON(w, out)
				return
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
		}
	case "my_groups":
		if len(parts) == 2 && r.Method == http.MethodGet {
			resp, err := a.opts.MeshServer.ListMyGroups(r.Context(), connection, serverID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, resp)
			return
		}
	case "members":
		if len(parts) == 2 && r.Method == http.MethodGet {
			afterMemberID := uint64(0)
			limit := uint32(50)
			if v := strings.TrimSpace(r.URL.Query().Get("after_member_id")); v != "" {
				if parsed, err := strconv.ParseUint(v, 10, 64); err == nil {
					afterMemberID = parsed
				}
			}
			if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
				if parsed, err := strconv.ParseUint(v, 10, 32); err == nil {
					limit = uint32(parsed)
				}
			}
			resp, err := a.opts.MeshServer.ListServerMembers(r.Context(), connection, serverID, afterMemberID, limit)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, resp)
			return
		}
	case "my_permissions":
		if len(parts) == 2 && r.Method == http.MethodGet {
			resp, err := a.opts.MeshServer.GetMyPermissions(r.Context(), connection, serverID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, resp)
			return
		}
	case "join":
		if len(parts) == 2 && r.Method == http.MethodPost {
			resp, err := a.opts.MeshServer.JoinServer(r.Context(), connection, serverID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, resp)
			return
		}
	case "invite":
		if len(parts) == 2 && r.Method == http.MethodPost {
			var body struct {
				TargetUserID string `json:"target_user_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
				http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
			resp, err := a.opts.MeshServer.InviteServerMember(r.Context(), connection, serverID, body.TargetUserID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, resp)
			return
		}
	case "kick":
		if len(parts) == 2 && r.Method == http.MethodPost {
			var body struct {
				TargetUserID string `json:"target_user_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
				http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
			resp, err := a.opts.MeshServer.KickServerMember(r.Context(), connection, serverID, body.TargetUserID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, resp)
			return
		}
	case "ban":
		if len(parts) == 2 && r.Method == http.MethodPost {
			var body struct {
				TargetUserID string `json:"target_user_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
				http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
			resp, err := a.opts.MeshServer.BanServerMember(r.Context(), connection, serverID, body.TargetUserID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, resp)
			return
		}
	case "groups":
		if len(parts) == 2 && r.Method == http.MethodPost {
			var body struct {
				Name            string `json:"name"`
				Description     string `json:"description"`
				Visibility      string `json:"visibility"`
				SlowModeSeconds uint32 `json:"slow_mode_seconds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
			resp, err := a.opts.MeshServer.CreateGroup(r.Context(), connection, serverID, body.Name, body.Description, parseMeshServerVisibility(body.Visibility), body.SlowModeSeconds)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			type channelSummaryWithID struct {
				ID              int                   `json:"id"`
				ChannelId       uint32                `json:"channel_id"`
				ServerId        uint32                `json:"server_id"`
				Type            sessionv1.ChannelType `json:"type"`
				Name            string                `json:"name"`
				Description     string                `json:"description"`
				Visibility      sessionv1.Visibility  `json:"visibility"`
				SlowModeSeconds uint32                `json:"slow_mode_seconds"`
				LastSeq         uint64                `json:"last_seq"`
				CanView         bool                  `json:"can_view"`
				CanSendMessage  bool                  `json:"can_send_message"`
				CanSendImage    bool                  `json:"can_send_image"`
				CanSendFile     bool                  `json:"can_send_file"`
			}
			type createGroupRespWithID struct {
				Ok        bool                  `json:"ok"`
				ServerID  uint32                `json:"server_id"`
				ChannelID uint32                `json:"channel_id"`
				Channel   *channelSummaryWithID `json:"channel"`
				Message   string                `json:"message"`
			}
			out := &createGroupRespWithID{
				Ok:        resp.Ok,
				ServerID:  resp.SpaceId,
				ChannelID: resp.ChannelId,
				Message:   resp.Message,
			}
			if resp.Channel != nil {
				chID := a.meshChannelIntID(resp.Channel.ChannelId)
				out.Channel = &channelSummaryWithID{
					ID:              chID,
					ChannelId:       resp.Channel.ChannelId,
					ServerId:        resp.Channel.SpaceId,
					Type:            resp.Channel.Type,
					Name:            resp.Channel.Name,
					Description:     resp.Channel.Description,
					Visibility:      resp.Channel.Visibility,
					SlowModeSeconds: resp.Channel.SlowModeSeconds,
					LastSeq:         resp.Channel.LastSeq,
					CanView:         resp.Channel.CanView,
					CanSendMessage:  resp.Channel.CanSendMessage,
					CanSendImage:    resp.Channel.CanSendImage,
					CanSendFile:     resp.Channel.CanSendFile,
				}
			}
			writeJSON(w, out)
			return
		}
	}
	http.NotFound(w, r)
}

func (a *LocalAPI) handleMeshServerChannelItem(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.MeshServer == nil {
		http.Error(w, "meshserver client not available", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/meshserver/channels/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	channelID := parts[0]
	action := parts[1]
	connection := strings.TrimSpace(r.URL.Query().Get("connection"))
	switch action {
	case "join":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			LastSeenSeq uint64 `json:"last_seen_seq"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		resp, err := a.opts.MeshServer.SubscribeChannel(r.Context(), connection, channelID, body.LastSeenSeq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, resp)
		return
	case "leave":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp, err := a.opts.MeshServer.UnsubscribeChannel(r.Context(), connection, channelID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, resp)
		return
	case "messages":
		if r.Method == http.MethodPost {
			// Supported:
			// - POST /messages (JSON): text/system messages
			// - POST /messages/image (multipart): image upload
			// - POST /messages/file (multipart): file upload
			if len(parts) == 2 {
				var body struct {
					ClientMsgID string `json:"client_msg_id"`
					MessageType string `json:"message_type"`
					Text        string `json:"text"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
					return
				}
				if body.ClientMsgID == "" {
					body.ClientMsgID = uuid.NewString()
				}

				msgType := sessionv1.MessageType_TEXT
				switch strings.ToLower(strings.TrimSpace(body.MessageType)) {
				case "image", "file":
					http.Error(w, "use /messages/image or /messages/file for uploads", http.StatusBadRequest)
					return
				case "system":
					msgType = sessionv1.MessageType_SYSTEM
				case "", "text":
					msgType = sessionv1.MessageType_TEXT
				default:
					msgType = sessionv1.MessageType_TEXT
				}

				resp, err := a.opts.MeshServer.SendMessage(r.Context(), connection, channelID, body.ClientMsgID, msgType, body.Text, nil, nil)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, resp)
				return
			}

			if len(parts) == 3 && parts[2] == "image" {
				if err := r.ParseMultipartForm(chat.MaxChatFileBytes + (1 << 20)); err != nil {
					http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
					return
				}
				clientMsgID := strings.TrimSpace(r.FormValue("client_msg_id"))
				if clientMsgID == "" {
					clientMsgID = uuid.NewString()
				}
				text := r.FormValue("text")

				file, header, err := r.FormFile("image")
				if err != nil {
					// allow fallback field name
					file, header, err = r.FormFile("file")
				}
				if err != nil {
					http.Error(w, "missing image file: "+err.Error(), http.StatusBadRequest)
					return
				}
				defer file.Close()
				data, err := chat.ValidateChatFileData(file)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}

				mimeType := http.DetectContentType(data)
				media := &sessionv1.MediaImage{
					MimeType:     mimeType,
					Size:         uint64(len(data)),
					InlineData:   data,
					OriginalName: header.Filename,
				}

				resp, err := a.opts.MeshServer.SendMessage(r.Context(), connection, channelID, clientMsgID, sessionv1.MessageType_IMAGE, text, []*sessionv1.MediaImage{media}, nil)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, resp)
				return
			}

			if len(parts) == 3 && parts[2] == "file" {
				if err := r.ParseMultipartForm(chat.MaxChatFileBytes + (1 << 20)); err != nil {
					http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
					return
				}
				clientMsgID := strings.TrimSpace(r.FormValue("client_msg_id"))
				if clientMsgID == "" {
					clientMsgID = uuid.NewString()
				}
				text := r.FormValue("text")

				file, header, err := r.FormFile("file")
				if err != nil {
					http.Error(w, "missing file: "+err.Error(), http.StatusBadRequest)
					return
				}
				defer file.Close()
				data, err := chat.ValidateChatFileData(file)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}

				mimeType := http.DetectContentType(data)
				media := &sessionv1.MediaFile{
					FileName:   header.Filename,
					MimeType:   mimeType,
					Size:       uint64(len(data)),
					InlineData: data,
				}

				resp, err := a.opts.MeshServer.SendMessage(r.Context(), connection, channelID, clientMsgID, sessionv1.MessageType_FILE, text, nil, []*sessionv1.MediaFile{media})
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, resp)
				return
			}

			http.NotFound(w, r)
			return
		}
	case "sync":
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		afterSeq := uint64(0)
		limit := uint32(50)
		if v := strings.TrimSpace(r.URL.Query().Get("after_seq")); v != "" {
			if parsed, err := strconv.ParseUint(v, 10, 64); err == nil {
				afterSeq = parsed
			}
		}
		if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
			if parsed, err := strconv.ParseUint(v, 10, 32); err == nil {
				limit = uint32(parsed)
			}
		}
		resp, err := a.opts.MeshServer.SyncChannel(r.Context(), connection, channelID, afterSeq, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, resp)
		return
	}
	http.NotFound(w, r)
}

func (a *LocalAPI) handleMeshServerConnections(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.MeshServer == nil {
		http.Error(w, "meshserver client not available", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"connections": a.opts.MeshServer.ListConnections()})
	case http.MethodPost:
		var body struct {
			Name        string `json:"name"`
			PeerID      string `json:"peer_id"`
			ClientAgent string `json:"client_agent"`
			ProtocolID  string `json:"protocol_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		info, err := a.opts.MeshServer.Connect(r.Context(), body.Name, body.PeerID, body.ClientAgent, body.ProtocolID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "connection": info})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *LocalAPI) handleMeshServerConnectionItem(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.MeshServer == nil {
		http.Error(w, "meshserver client not available", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/meshserver/connections/")
	name := strings.Trim(strings.TrimSpace(path), "/")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		// Return the connection's current status snapshot.
		for _, c := range a.opts.MeshServer.ListConnections() {
			if strings.EqualFold(strings.TrimSpace(c.Name), name) {
				writeJSON(w, c)
				return
			}
		}
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	case http.MethodDelete:
		if err := a.opts.MeshServer.Disconnect(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "name": name})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleMeshServerServerMyPermissions returns connection/server-level permissions.
//
// GET /api/v1/meshserver/server/my_permissions?connection=...
// - no space_id param; call GET_CREATE_SPACE_PERMISSIONS directly by connection.
func (a *LocalAPI) handleMeshServerServerMyPermissions(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.MeshServer == nil {
		http.Error(w, "meshserver client not available", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	connection := strings.TrimSpace(r.URL.Query().Get("connection"))
	connections := a.opts.MeshServer.ListConnections()
	if len(connections) == 0 {
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	}
	if connection == "" {
		if len(connections) == 1 {
			connection = connections[0].Name
		} else {
			http.Error(w, "connection is required when multiple connections exist", http.StatusBadRequest)
			return
		}
	}

	resp, err := a.opts.MeshServer.GetCreateSpacePermissions(r.Context(), connection)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Proto json tags use omitempty on can_create_space; always expose it for API clarity.
	writeJSON(w, map[string]any{
		"ok":               resp.Ok,
		"can_create_space": resp.CanCreateSpace,
		"message":          resp.Message,
	})
}

// handleMeshServerHTTPAccessToken proxies meshserver HTTP JWT auth (challenge + verify) using local libp2p identity.
// POST /api/v1/meshserver/http/access_token
func (a *LocalAPI) handleMeshServerHTTPAccessToken(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.MeshServer == nil {
		http.Error(w, "meshserver client not available", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		BaseURL    string `json:"base_url"`
		ProtocolID string `json:"protocol_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	baseURL := strings.TrimSpace(body.BaseURL)
	if baseURL == "" {
		http.Error(w, "base_url is required", http.StatusBadRequest)
		return
	}
	out, err := a.opts.MeshServer.FetchMeshServerHTTPAccessToken(r.Context(), baseURL, strings.TrimSpace(body.ProtocolID))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, out)
}

func parseMeshServerVisibility(value string) sessionv1.Visibility {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "public":
		return sessionv1.Visibility_PUBLIC
	default:
		return sessionv1.Visibility_PRIVATE
	}
}

func (a *LocalAPI) handleChatNetworkStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	writeJSON(w, a.opts.ChatService.NetworkStatus())
}

func (a *LocalAPI) handleChatWebSocket(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type eventSubscriber interface {
		SubscribeChatEvents() (<-chan chat.ChatEvent, func())
	}

	sub, ok := a.opts.ChatService.(eventSubscriber)
	if !ok {
		http.Error(w, "chat events not available", http.StatusNotFound)
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			// Console/chat are usually served on same host; for convenience we allow all origins.
			return true
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	eventsCh, unsubscribe := sub.SubscribeChatEvents()
	defer unsubscribe()

	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})
	// Drain incoming messages to detect close; we don't need client -> server messages for now.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case evt, ok := <-eventsCh:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(evt); err != nil {
				return
			}
		case <-done:
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (a *LocalAPI) handleChatPeerRoutes(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.ChatService == nil {
		http.Error(w, "chat service not available", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/chat/peers/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	peerID := parts[0]
	action := parts[1]
	switch action {
	case "status":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp, err := a.opts.ChatService.PeerStatus(peerID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, resp)
	case "connect":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := a.opts.ChatService.ConnectPeer(peerID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "connected"})
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "encode response error", http.StatusInternalServerError)
		return
	}
}
