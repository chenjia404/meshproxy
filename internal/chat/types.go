package chat

import (
	"time"

	"github.com/chenjia404/meshproxy/internal/binding"
)

const (
	MessageTypeSessionRequest        = "session_request"
	MessageTypeSessionAccept         = "session_accept"
	MessageTypeSessionReject         = "session_reject"
	MessageTypeSessionAcceptAck      = "session_accept_ack"
	MessageTypeProfileSync           = "profile_sync"
	MessageTypeAvatarRequest         = "avatar_request"
	MessageTypeAvatarResponse        = "avatar_response"
	MessageTypeChatText              = "chat_text"
	MessageTypeChatFile              = "chat_file"
	MessageTypeChatFileFetchRequest  = "chat_file_fetch_request"
	MessageTypeChatFileFetchResponse = "chat_file_fetch_response"
	MessageTypeGroupInviteNote       = "group_invite_notice"
	MessageTypeGroupInviteNotice     = MessageTypeGroupInviteNote
	MessageTypeChatSyncRequest       = "chat_sync_request"
	MessageTypeChatSyncResponse      = "chat_sync_response"
	MessageTypeDeliveryAck           = "delivery_ack"
	MessageTypeMessageRevoke         = "message_revoke"
	MessageTypeRetentionUpdate       = "retention_update"
	MessageTypeRetentionAck          = "retention_ack"
	MessageTypeBindingSync           = binding.MessageTypeBindingSync
)

const (
	RequestStatePending  = "pending"
	RequestStateAccepted = "accepted"
	RequestStateRejected = "rejected"
	RequestStateBlocked  = "blocked"
)

const (
	ConversationStateActive = "active"
	// ConversationStateNoFriend means the friend relationship isn't established
	// (e.g. we got a SessionReject after sending messages).
	ConversationStateNoFriend = "no_friend"
)

const (
	TransportModeDirect       = "direct"
	TransportModeOfflineStore = "offline_store" // 來自離線 store 拉取（可選跳過序號空洞）
)

const (
	MessageStateLocalOnly       = "local_only"
	MessageStateSentToTransport = "sent_to_transport"
	MessageStateDeliveredRemote = "delivered_remote"
	MessageStateReceived        = "received"
	MessageStateQueuedForRetry  = "queued_for_retry"
)

const (
	MaxProfileBioLength     = 140
	MaxProfileAvatarBytes   = 512 << 10
	DefaultAvatarStorageDir = "avatar"
)

type Profile struct {
	PeerID     string    `json:"peer_id"`
	Nickname   string    `json:"nickname"`
	Bio        string    `json:"bio"`
	Avatar     string    `json:"avatar"`
	AvatarCID  string    `json:"avatar_cid,omitempty"`
	ChatKexPub string    `json:"chat_kex_pub"`
	CreatedAt  time.Time `json:"created_at"`

	// bindingRecord 由 store 從 binding_record_json 載入，供 P2P 同步等內部邏輯使用，不參與 JSON。
	bindingRecord *binding.BindingRecord `json:"-"`
	// BindingEthAddress 冗餘欄位，供 API（如 GET /chat/me）展示；完整記錄見庫內 binding_record_json。
	BindingEthAddress string `json:"binding_eth_address,omitempty"`
	BindingUpdatedAt  int64  `json:"-"` // Unix 毫秒；不對外 JSON
}

type Contact struct {
	PeerID           string    `json:"peer_id"`
	Nickname         string    `json:"nickname"`
	Bio              string    `json:"bio"`
	Avatar           string    `json:"avatar"`
	CID              string    `json:"cid,omitempty"`
	RemoteNickname   string    `json:"remote_nickname,omitempty"`
	RetentionMinutes int       `json:"retention_minutes"`
	Blocked          bool      `json:"blocked"`
	LastSeenAt       time.Time `json:"last_seen_at"`
	UpdatedAt        time.Time `json:"updated_at"`

	// 以下欄位由 store 載入，僅內部／除錯使用，API（GET /chat/contacts）只對外回傳 binding_eth_address。
	bindingRecord      *binding.BindingRecord `json:"-"`
	BindingStatus      string                 `json:"-"`
	BindingValidatedAt int64                  `json:"-"`
	BindingError       string                 `json:"-"`
	BindingEthAddress  string                 `json:"binding_eth_address,omitempty"`
}

// ContactBindingDetails 為 GET /chat/contacts/{peer_id}/binding 回應（完整 BindingRecord 與本機校驗狀態）。
type ContactBindingDetails struct {
	PeerID             string                 `json:"peer_id"`
	Binding            *binding.BindingRecord `json:"binding,omitempty"`
	BindingStatus      string                 `json:"binding_status,omitempty"`
	BindingValidatedAt int64                  `json:"binding_validated_at,omitempty"`
	BindingError       string                 `json:"binding_error,omitempty"`
}

func contactBindingDetailsFrom(c Contact) ContactBindingDetails {
	return ContactBindingDetails{
		PeerID:             c.PeerID,
		Binding:            c.bindingRecord,
		BindingStatus:      c.BindingStatus,
		BindingValidatedAt: c.BindingValidatedAt,
		BindingError:       c.BindingError,
	}
}

type Request struct {
	RequestID         string    `json:"request_id"`
	FromPeerID        string    `json:"from_peer_id"`
	ToPeerID          string    `json:"to_peer_id"`
	State             string    `json:"state"`
	IntroText         string    `json:"intro_text"`
	Nickname          string    `json:"nickname"`
	Bio               string    `json:"bio"`
	Avatar            string    `json:"avatar"`
	RetentionMinutes  int       `json:"retention_minutes"`
	RemoteChatKexPub  string    `json:"remote_chat_kex_pub"`
	ConversationID    string    `json:"conversation_id,omitempty"`
	LastTransportMode string    `json:"last_transport_mode"`
	RetryCount        int       `json:"retry_count,omitempty"`
	NextRetryAt       time.Time `json:"next_retry_at,omitempty"`
	RetryJobStatus    string    `json:"retry_job_status,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Conversation struct {
	ConversationID     string    `json:"conversation_id"`
	PeerID             string    `json:"peer_id"`
	State              string    `json:"state"`
	LastMessageAt      time.Time `json:"last_message_at"`
	LastMessage        string    `json:"last_message,omitempty"`
	LastTransportMode  string    `json:"last_transport_mode"`
	UnreadCount        int       `json:"unread_count"`
	RetentionMinutes   int       `json:"retention_minutes"`
	RetentionSyncState string    `json:"retention_sync_state"`
	RetentionSyncedAt  time.Time `json:"retention_synced_at,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type Message struct {
	MsgID          string    `json:"msg_id"`
	ConversationID string    `json:"conversation_id"`
	SenderPeerID   string    `json:"sender_peer_id"`
	ReceiverPeerID string    `json:"receiver_peer_id"`
	Direction      string    `json:"direction"`
	MsgType        string    `json:"msg_type"`
	Plaintext      string    `json:"plaintext"`
	FileName       string    `json:"file_name,omitempty"`
	MIMEType       string    `json:"mime_type,omitempty"`
	FileSize       int64     `json:"file_size,omitempty"`
	FileCID        string    `json:"file_cid,omitempty"`
	TransportMode  string    `json:"transport_mode"`
	State          string    `json:"state"`
	Counter        uint64    `json:"counter"`
	CreatedAt      time.Time `json:"created_at"`
	DeliveredAt    time.Time `json:"delivered_at,omitempty"`
}

type SessionRequest struct {
	Type             string `json:"type"`
	RequestID        string `json:"request_id"`
	FromPeerID       string `json:"from_peer_id"`
	ToPeerID         string `json:"to_peer_id"`
	Nickname         string `json:"nickname"`
	Bio              string `json:"bio"`
	AvatarName       string `json:"avatar_name"`
	RetentionMinutes int    `json:"retention_minutes"`
	IntroText        string `json:"intro_text"`
	ChatKexPub       string `json:"chat_kex_pub"`
	SentAtUnix       int64  `json:"sent_at_unix"`
	Signature        []byte `json:"signature,omitempty"`
}

type SessionAccept struct {
	Type             string `json:"type"`
	RequestID        string `json:"request_id"`
	ConversationID   string `json:"conversation_id"`
	FromPeerID       string `json:"from_peer_id"`
	ToPeerID         string `json:"to_peer_id"`
	Bio              string `json:"bio"`
	AvatarName       string `json:"avatar_name"`
	RetentionMinutes int    `json:"retention_minutes"`
	ChatKexPub       string `json:"chat_kex_pub"`
	SentAtUnix       int64  `json:"sent_at_unix"`
	Signature        []byte `json:"signature,omitempty"`
}

// SessionAcceptAck is sent back by the receiver of SessionAccept,
// so the sender can confirm that the conversation/session is established.
type SessionAcceptAck struct {
	Type           string `json:"type"`
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	SentAtUnix     int64  `json:"sent_at_unix"`
	Signature      []byte `json:"signature,omitempty"`
}

type SessionReject struct {
	Type       string `json:"type"`
	RequestID  string `json:"request_id"`
	FromPeerID string `json:"from_peer_id"`
	ToPeerID   string `json:"to_peer_id"`
	SentAtUnix int64  `json:"sent_at_unix"`
	Signature  []byte `json:"signature,omitempty"`
}

type ProfileSync struct {
	Type              string `json:"type"`
	FromPeerID        string `json:"from_peer_id"`
	ToPeerID          string `json:"to_peer_id"`
	Nickname          string `json:"nickname"`
	Bio               string `json:"bio"`
	AvatarName        string `json:"avatar_name"`
	BindingEthAddress string `json:"binding_eth_address,omitempty"`
	SentAtUnix        int64  `json:"sent_at_unix"`
	Signature         []byte `json:"signature,omitempty"`
}

type AvatarRequest struct {
	Type       string `json:"type"`
	FromPeerID string `json:"from_peer_id"`
	ToPeerID   string `json:"to_peer_id"`
	AvatarName string `json:"avatar_name"`
	SentAtUnix int64  `json:"sent_at_unix"`
	Signature  []byte `json:"signature,omitempty"`
}

type AvatarResponse struct {
	Type       string `json:"type"`
	FromPeerID string `json:"from_peer_id"`
	ToPeerID   string `json:"to_peer_id"`
	AvatarName string `json:"avatar_name"`
	AvatarData []byte `json:"avatar_data,omitempty"`
	SentAtUnix int64  `json:"sent_at_unix"`
	Signature  []byte `json:"signature,omitempty"`
}

type ChatText struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	MsgID          string `json:"msg_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	Ciphertext     []byte `json:"ciphertext"`
	Counter        uint64 `json:"counter"`
	SentAtUnix     int64  `json:"sent_at_unix"`
	Signature      []byte `json:"signature,omitempty"`
}

type ChatFile struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	MsgID          string `json:"msg_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	FileName       string `json:"file_name"`
	MIMEType       string `json:"mime_type"`
	FileSize       int64  `json:"file_size"`
	FileCID        string `json:"file_cid,omitempty"`
	Ciphertext     []byte `json:"ciphertext"`
	Counter        uint64 `json:"counter"`
	SentAtUnix     int64  `json:"sent_at_unix"`
	Signature      []byte `json:"signature,omitempty"`
}

type ChatFileFetchRequest struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	MsgID          string `json:"msg_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	Offset         uint64 `json:"offset"`
	ChunkSize      int    `json:"chunk_size"`
	SentAtUnix     int64  `json:"sent_at_unix"`
	Signature      []byte `json:"signature,omitempty"`
}

type ChatFileFetchResponse struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	MsgID          string `json:"msg_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	Offset         uint64 `json:"offset"`
	Eof            bool   `json:"eof"`
	FileSize       int64  `json:"file_size"`
	FileCID        string `json:"file_cid,omitempty"`
	Ciphertext     []byte `json:"ciphertext,omitempty"`
	Error          string `json:"error,omitempty"`
	SentAtUnix     int64  `json:"sent_at_unix"`
	Signature      []byte `json:"signature,omitempty"`
}

type DeliveryAck struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	MsgID          string `json:"msg_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	AckedAtUnix    int64  `json:"acked_at_unix"`
	Signature      []byte `json:"signature,omitempty"`
}

type ChatSyncRequest struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	NextCounter    uint64 `json:"next_counter"`
	SentAtUnix     int64  `json:"sent_at_unix"`
	Signature      []byte `json:"signature,omitempty"`
}

type ChatSyncResponse struct {
	Type              string     `json:"type"`
	ConversationID    string     `json:"conversation_id"`
	FromPeerID        string     `json:"from_peer_id,omitempty"`
	RemoteSendCounter uint64     `json:"remote_send_counter,omitempty"`
	Messages          []ChatText `json:"messages,omitempty"`
	Files             []ChatFile `json:"files,omitempty"`
	Signature         []byte     `json:"signature,omitempty"`
}

type MessageRevoke struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	MsgID          string `json:"msg_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	RevokedAtUnix  int64  `json:"revoked_at_unix"`
	Signature      []byte `json:"signature,omitempty"`
}

type RetentionAck struct {
	Type             string `json:"type"`
	ConversationID   string `json:"conversation_id"`
	FromPeerID       string `json:"from_peer_id"`
	ToPeerID         string `json:"to_peer_id"`
	RetentionMinutes int    `json:"retention_minutes"`
	AckedAtUnix      int64  `json:"acked_at_unix"`
	Signature        []byte `json:"signature,omitempty"`
}

type RetentionUpdate struct {
	Type             string `json:"type"`
	ConversationID   string `json:"conversation_id"`
	FromPeerID       string `json:"from_peer_id"`
	ToPeerID         string `json:"to_peer_id"`
	RetentionMinutes int    `json:"retention_minutes"`
	UpdatedAtUnix    int64  `json:"updated_at_unix"`
	Signature        []byte `json:"signature,omitempty"`
}
