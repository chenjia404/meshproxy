package api

import (
	"context"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenjia404/meshproxy/internal/chat"
	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/discovery"
	"github.com/chenjia404/meshproxy/internal/exit"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/update"
)

//go:generate go run ../tools/syncconsole
//go:generate go run ../tools/syncchatui
//go:embed console/* chat/*
var consoleFS embed.FS

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
	UpdateService  UpdateProvider
	UpdateSettings UpdateSettingsProvider
}

type UpdateProvider interface {
	CheckForUpdate(ctx context.Context) (update.Info, error)
	ApplyUpdate(ctx context.Context) (update.ApplyResult, error)
}

type UpdateSettingsProvider interface {
	GetAutoUpdate() bool
	SetAutoUpdate(enabled bool) error
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
	UpdateConversationRetention(conversationID string, minutes int) (chat.Conversation, error)
	ListMessages(conversationID string) ([]chat.Message, error)
	SyncConversation(conversationID string) error
	RevokeMessage(conversationID, msgID string) error
	SendFile(conversationID, fileName, mimeType string, data []byte) (chat.Message, error)
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
	Close() error
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
	chatHTML        []byte // embedded chat index.html, served directly to avoid redirect
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
	mux.HandleFunc("/api/v1/chat/avatars/", api.handleChatAvatar)
	mux.HandleFunc("/api/v1/chat/contacts", api.handleChatContacts)
	mux.HandleFunc("/api/v1/chat/contacts/", api.handleChatContactItem)
	mux.HandleFunc("/api/v1/chat/requests", api.handleChatRequests)
	mux.HandleFunc("/api/v1/chat/requests/", api.handleChatRequestItem)
	mux.HandleFunc("/api/v1/chat/conversations", api.handleChatConversations)
	mux.HandleFunc("/api/v1/chat/conversations/", api.handleChatConversationItem)
	mux.HandleFunc("/api/v1/chat/network/status", api.handleChatNetworkStatus)
	mux.HandleFunc("/api/v1/chat/peers/", api.handleChatPeerRoutes)
	mux.HandleFunc("/api/v1/groups", api.handleGroups)
	mux.HandleFunc("/api/v1/groups/", api.handleGroupItem)
	mux.HandleFunc("/api/v1/update/check", api.handleUpdateCheck)
	mux.HandleFunc("/api/v1/update/apply", api.handleUpdateApply)
	mux.HandleFunc("/api/v1/update/settings", api.handleUpdateSettings)
	if opts != nil && opts.ExitService != nil && opts.ExitService.Policy != nil {
		mux.HandleFunc("/api/v1/exit/policy", api.handleExitPolicy)
		mux.HandleFunc("/api/v1/exit/status", api.handleExitStatus)
		mux.HandleFunc("/api/v1/exit/drain", api.handleExitDrain)
		mux.HandleFunc("/api/v1/exit/resume", api.handleExitResume)
	}

	// Console: serve index.html directly from embed (no FileServer, no redirect)
	var consoleHTML []byte
	if sub, err := fs.Sub(consoleFS, "console"); err == nil {
		consoleHTML, _ = fs.ReadFile(sub, "index.html")
	}
	api.consoleHTML = consoleHTML
	var chatHTML []byte
	if sub, err := fs.Sub(consoleFS, "chat"); err == nil {
		chatHTML, _ = fs.ReadFile(sub, "index.html")
	}
	api.chatHTML = chatHTML

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
			if len(api.chatHTML) > 0 {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				w.Write(api.chatHTML)
				return
			}
			http.NotFound(w, r)
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
	writeJSON(w, resp)
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
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	peerID, action := parts[0], parts[1]
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
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	conversationID, action := parts[0], parts[1]
	switch action {
	case "sync":
		if len(parts) != 2 || r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := a.opts.ChatService.SyncConversation(conversationID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"conversation_id": conversationID,
			"status":          "sync_requested",
		})
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
			msgs, err := a.opts.ChatService.ListMessages(conversationID)
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
			msg, err := a.opts.ChatService.SendFile(conversationID, fileName, mimeType, data)
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
