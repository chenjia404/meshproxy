package chat

import "time"

const (
	MessageTypeSessionRequest    = "session_request"
	MessageTypeSessionAccept     = "session_accept"
	MessageTypeSessionReject     = "session_reject"
	MessageTypeChatText          = "chat_text"
	MessageTypeChatFile          = "chat_file"
	MessageTypeGroupInviteNote   = "group_invite_notice"
	MessageTypeGroupInviteNotice = MessageTypeGroupInviteNote
	MessageTypeChatSyncRequest   = "chat_sync_request"
	MessageTypeChatSyncResponse  = "chat_sync_response"
	MessageTypeDeliveryAck       = "delivery_ack"
	MessageTypeMessageRevoke     = "message_revoke"
	MessageTypeRetentionUpdate   = "retention_update"
	MessageTypeRetentionAck      = "retention_ack"
)

const (
	RequestStatePending  = "pending"
	RequestStateAccepted = "accepted"
	RequestStateRejected = "rejected"
	RequestStateBlocked  = "blocked"
)

const (
	ConversationStateActive = "active"
)

const (
	TransportModeDirect = "direct"
)

const (
	MessageStateLocalOnly       = "local_only"
	MessageStateSentToTransport = "sent_to_transport"
	MessageStateDeliveredRemote = "delivered_remote"
	MessageStateReceived        = "received"
	MessageStateQueuedForRetry  = "queued_for_retry"
)

type Profile struct {
	PeerID     string    `json:"peer_id"`
	Nickname   string    `json:"nickname"`
	ChatKexPub string    `json:"chat_kex_pub"`
	CreatedAt  time.Time `json:"created_at"`
}

type Contact struct {
	PeerID         string    `json:"peer_id"`
	Nickname       string    `json:"nickname"`
	RemoteNickname string    `json:"remote_nickname,omitempty"`
	Blocked        bool      `json:"blocked"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Request struct {
	RequestID         string    `json:"request_id"`
	FromPeerID        string    `json:"from_peer_id"`
	ToPeerID          string    `json:"to_peer_id"`
	State             string    `json:"state"`
	IntroText         string    `json:"intro_text"`
	Nickname          string    `json:"nickname"`
	RemoteChatKexPub  string    `json:"remote_chat_kex_pub"`
	ConversationID    string    `json:"conversation_id,omitempty"`
	LastTransportMode string    `json:"last_transport_mode"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Conversation struct {
	ConversationID     string    `json:"conversation_id"`
	PeerID             string    `json:"peer_id"`
	State              string    `json:"state"`
	LastMessageAt      time.Time `json:"last_message_at"`
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
	TransportMode  string    `json:"transport_mode"`
	State          string    `json:"state"`
	Counter        uint64    `json:"counter"`
	CreatedAt      time.Time `json:"created_at"`
	DeliveredAt    time.Time `json:"delivered_at,omitempty"`
}

type SessionRequest struct {
	Type       string `json:"type"`
	RequestID  string `json:"request_id"`
	FromPeerID string `json:"from_peer_id"`
	ToPeerID   string `json:"to_peer_id"`
	Nickname   string `json:"nickname"`
	IntroText  string `json:"intro_text"`
	ChatKexPub string `json:"chat_kex_pub"`
	SentAtUnix int64  `json:"sent_at_unix"`
}

type SessionAccept struct {
	Type           string `json:"type"`
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	ChatKexPub     string `json:"chat_kex_pub"`
	SentAtUnix     int64  `json:"sent_at_unix"`
}

type SessionReject struct {
	Type       string `json:"type"`
	RequestID  string `json:"request_id"`
	FromPeerID string `json:"from_peer_id"`
	ToPeerID   string `json:"to_peer_id"`
	SentAtUnix int64  `json:"sent_at_unix"`
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
	Ciphertext     []byte `json:"ciphertext"`
	Counter        uint64 `json:"counter"`
	SentAtUnix     int64  `json:"sent_at_unix"`
}

type DeliveryAck struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	MsgID          string `json:"msg_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	AckedAtUnix    int64  `json:"acked_at_unix"`
}

type ChatSyncRequest struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	NextCounter    uint64 `json:"next_counter"`
	SentAtUnix     int64  `json:"sent_at_unix"`
}

type ChatSyncResponse struct {
	Type              string     `json:"type"`
	ConversationID    string     `json:"conversation_id"`
	RemoteSendCounter uint64     `json:"remote_send_counter,omitempty"`
	Messages          []ChatText `json:"messages,omitempty"`
	Files             []ChatFile `json:"files,omitempty"`
}

type MessageRevoke struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	MsgID          string `json:"msg_id"`
	FromPeerID     string `json:"from_peer_id"`
	ToPeerID       string `json:"to_peer_id"`
	RevokedAtUnix  int64  `json:"revoked_at_unix"`
}

type RetentionAck struct {
	Type             string `json:"type"`
	ConversationID   string `json:"conversation_id"`
	FromPeerID       string `json:"from_peer_id"`
	ToPeerID         string `json:"to_peer_id"`
	RetentionMinutes int    `json:"retention_minutes"`
	AckedAtUnix      int64  `json:"acked_at_unix"`
}

type RetentionUpdate struct {
	Type             string `json:"type"`
	ConversationID   string `json:"conversation_id"`
	FromPeerID       string `json:"from_peer_id"`
	ToPeerID         string `json:"to_peer_id"`
	RetentionMinutes int    `json:"retention_minutes"`
	UpdatedAtUnix    int64  `json:"updated_at_unix"`
}
