package chat

import (
	"encoding/json"
	"time"
)

const (
	MessageTypeGroupControl      = "group_control"
	MessageTypeGroupJoinRequest  = "group_join_request"
	MessageTypeGroupLeaveRequest = "group_leave_request"
	MessageTypeGroupChatText     = "group_chat_text"
	MessageTypeGroupChatFile     = "group_chat_file"
	MessageTypeGroupDeliveryAck  = "group_delivery_ack"
	MessageTypeGroupSyncRequest  = "group_sync_request"
	MessageTypeGroupSyncResponse = "group_sync_response"
)

const (
	GroupRoleController = "controller"
	GroupRoleAdmin      = "admin"
	GroupRoleMember     = "member"
)

const (
	GroupStateActive   = "active"
	GroupStateArchived = "archived"
)

const (
	GroupMemberStateInvited = "invited"
	GroupMemberStateActive  = "active"
	GroupMemberStateLeft    = "left"
	GroupMemberStateRemoved = "removed"
)

const (
	GroupEventCreate             = "group_create"
	GroupEventInvite             = "group_invite"
	GroupEventJoin               = "group_join"
	GroupEventLeave              = "group_leave"
	GroupEventRemove             = "group_remove"
	GroupEventTitleUpdate        = "group_title_update"
	GroupEventRetentionUpdate    = "group_retention_update"
	GroupEventMessageRevoke      = "group_message_revoke"
	GroupEventDissolve           = "group_dissolve"
	GroupEventControllerTransfer = "group_controller_transfer"
	GroupEventEpochRotate        = "group_epoch_rotate"
)

const MaxGroupTitleLen = 64

const (
	GroupMessageStateLocalOnly          = "local_only"
	GroupMessageStateSentToTransport    = "sent_to_transport"
	GroupMessageStateQueuedForRetry     = "queued_for_retry"
	GroupMessageStatePartiallySent      = "partially_sent"
	GroupMessageStateDeliveredRemote    = "delivered_remote"
	GroupMessageStatePartiallyDelivered = "partially_delivered"
	GroupMessageStateFailedPartial      = "failed_partial"
	GroupMessageStateReceived           = "received"
)

const (
	GroupDeliveryStatePending         = "pending"
	GroupDeliveryStateSentToTransport = "sent_to_transport"
	GroupDeliveryStateQueuedForRetry  = "queued_for_retry"
	GroupDeliveryStateDeliveredRemote = "delivered_remote"
	GroupDeliveryStateFailed          = "failed"
)

type Group struct {
	GroupID          string    `json:"group_id"`
	Title            string    `json:"title"`
	Avatar           string    `json:"avatar"`
	ControllerPeerID string    `json:"controller_peer_id"`
	CurrentEpoch     uint64    `json:"current_epoch"`
	RetentionMinutes int       `json:"retention_minutes"`
	State            string    `json:"state"`
	LastEventSeq     uint64    `json:"last_event_seq"`
	LastMessageAt    time.Time `json:"last_message_at"`
	MemberCount      int       `json:"member_count,omitempty"`
	LocalMemberRole  string    `json:"local_member_role,omitempty"`
	LocalMemberState string    `json:"local_member_state,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type GroupMember struct {
	GroupID     string    `json:"group_id"`
	PeerID      string    `json:"peer_id"`
	Role        string    `json:"role"`
	State       string    `json:"state"`
	InvitedBy   string    `json:"invited_by"`
	JoinedEpoch uint64    `json:"joined_epoch"`
	LeftEpoch   uint64    `json:"left_epoch"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type GroupEvent struct {
	EventID      string    `json:"event_id"`
	GroupID      string    `json:"group_id"`
	EventSeq     uint64    `json:"event_seq"`
	EventType    string    `json:"event_type"`
	ActorPeerID  string    `json:"actor_peer_id"`
	SignerPeerID string    `json:"signer_peer_id"`
	PayloadJSON  string    `json:"payload_json"`
	Signature    []byte    `json:"signature"`
	CreatedAt    time.Time `json:"created_at"`
}

type GroupEventDeliverySummary struct {
	Total           int `json:"total"`
	SentToTransport int `json:"sent_to_transport"`
	QueuedForRetry  int `json:"queued_for_retry"`
	Failed          int `json:"failed"`
}

type GroupEventView struct {
	EventID         string                    `json:"event_id"`
	GroupID         string                    `json:"group_id"`
	EventSeq        uint64                    `json:"event_seq"`
	EventType       string                    `json:"event_type"`
	ActorPeerID     string                    `json:"actor_peer_id"`
	SignerPeerID    string                    `json:"signer_peer_id"`
	PayloadJSON     string                    `json:"payload_json"`
	CreatedAt       time.Time                 `json:"created_at"`
	DeliverySummary GroupEventDeliverySummary `json:"delivery_summary"`
}

type GroupEpoch struct {
	GroupID            string    `json:"group_id"`
	Epoch              uint64    `json:"epoch"`
	WrappedKeyForLocal []byte    `json:"wrapped_key_for_local"`
	CreatedAt          time.Time `json:"created_at"`
}

type GroupDetails struct {
	Group        Group            `json:"group"`
	Members      []GroupMember    `json:"members"`
	RecentEvents []GroupEventView `json:"recent_events,omitempty"`
}

type GroupInviteNoticePayload struct {
	GroupID          string               `json:"group_id"`
	Title            string               `json:"title"`
	ControllerPeerID string               `json:"controller_peer_id"`
	InviteePeerID    string               `json:"invitee_peer_id"`
	InviteText       string               `json:"invite_text,omitempty"`
	EventID          string               `json:"event_id"`
	EventSeq         uint64               `json:"event_seq,omitempty"`
	CurrentEpoch     uint64               `json:"current_epoch"`
	LocalMemberState string               `json:"local_member_state,omitempty"`
	InviteEnvelope   GroupControlEnvelope `json:"invite_envelope"`
}

type GroupMessage struct {
	MsgID           string               `json:"msg_id"`
	GroupID         string               `json:"group_id"`
	Epoch           uint64               `json:"epoch"`
	SenderPeerID    string               `json:"sender_peer_id"`
	SenderSeq       uint64               `json:"sender_seq"`
	MsgType         string               `json:"msg_type"`
	Plaintext       string               `json:"plaintext"`
	FileName        string               `json:"file_name,omitempty"`
	MIMEType        string               `json:"mime_type,omitempty"`
	FileSize        int64                `json:"file_size,omitempty"`
	FileCID         string               `json:"file_cid,omitempty"`
	Signature       []byte               `json:"signature,omitempty"`
	State           string               `json:"state"`
	DeliverySummary GroupDeliverySummary `json:"delivery_summary,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
}

type GroupMessageDelivery struct {
	MsgID         string    `json:"msg_id"`
	PeerID        string    `json:"peer_id"`
	TransportMode string    `json:"transport_mode"`
	State         string    `json:"state"`
	RetryCount    int       `json:"retry_count"`
	NextRetryAt   time.Time `json:"next_retry_at,omitempty"`
	DeliveredAt   time.Time `json:"delivered_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type GroupDeliverySummary struct {
	Total           int `json:"total"`
	Pending         int `json:"pending"`
	SentToTransport int `json:"sent_to_transport"`
	QueuedForRetry  int `json:"queued_for_retry"`
	DeliveredRemote int `json:"delivered_remote"`
	Failed          int `json:"failed"`
}

type GroupControlEnvelope struct {
	Type          string          `json:"type"`
	EventType     string          `json:"event_type"`
	GroupID       string          `json:"group_id"`
	EventID       string          `json:"event_id"`
	EventSeq      uint64          `json:"event_seq"`
	ActorPeerID   string          `json:"actor_peer_id"`
	SignerPeerID  string          `json:"signer_peer_id"`
	CreatedAtUnix int64           `json:"created_at_unix"`
	Payload       json.RawMessage `json:"payload"`
	Signature     []byte          `json:"signature"`
}

type GroupCreatePayload struct {
	Title            string            `json:"title"`
	ControllerPeerID string            `json:"controller_peer_id"`
	InitialMembers   []string          `json:"initial_members"`
	Epoch            uint64            `json:"epoch"`
	EpochKeys        map[string][]byte `json:"epoch_keys,omitempty"`
}

type GroupInvitePayload struct {
	InviteePeerID    string   `json:"invitee_peer_id"`
	Role             string   `json:"role"`
	InviteText       string   `json:"invite_text"`
	Epoch            uint64   `json:"epoch"`
	Title            string   `json:"title"`
	ControllerPeerID string   `json:"controller_peer_id"`
	KnownMembers     []string `json:"known_members"`
	WrappedGroupKey  []byte   `json:"wrapped_group_key,omitempty"`
}

type GroupJoinPayload struct {
	JoinerPeerID  string `json:"joiner_peer_id"`
	AcceptedEpoch uint64 `json:"accepted_epoch"`
}

type GroupLeavePayload struct {
	LeaverPeerID string            `json:"leaver_peer_id"`
	Reason       string            `json:"reason"`
	NewEpoch     uint64            `json:"new_epoch"`
	EpochKeys    map[string][]byte `json:"epoch_keys,omitempty"`
}

type GroupRemovePayload struct {
	TargetPeerID string            `json:"target_peer_id"`
	Reason       string            `json:"reason"`
	NewEpoch     uint64            `json:"new_epoch"`
	EpochKeys    map[string][]byte `json:"epoch_keys,omitempty"`
}

type GroupTitleUpdatePayload struct {
	Title string `json:"title"`
}

type GroupRetentionUpdatePayload struct {
	RetentionMinutes int `json:"retention_minutes"`
}

type GroupMessageRevokePayload struct {
	MsgID        string `json:"msg_id"`
	SenderPeerID string `json:"sender_peer_id"`
}

type GroupDissolvePayload struct {
	Reason string `json:"reason"`
}

type GroupControllerTransferPayload struct {
	FromPeerID string            `json:"from_peer_id"`
	ToPeerID   string            `json:"to_peer_id"`
	NewEpoch   uint64            `json:"new_epoch"`
	EpochKeys  map[string][]byte `json:"epoch_keys,omitempty"`
}

type GroupEpochRotatePayload struct {
	OldEpoch uint64   `json:"old_epoch"`
	NewEpoch uint64   `json:"new_epoch"`
	Members  []string `json:"members"`
}

type GroupJoinRequest struct {
	Type          string `json:"type"`
	GroupID       string `json:"group_id"`
	JoinerPeerID  string `json:"joiner_peer_id"`
	AcceptedEpoch uint64 `json:"accepted_epoch"`
	SentAtUnix    int64  `json:"sent_at_unix"`
	Signature     []byte `json:"signature,omitempty"`
}

type GroupLeaveRequest struct {
	Type         string `json:"type"`
	GroupID      string `json:"group_id"`
	LeaverPeerID string `json:"leaver_peer_id"`
	Reason       string `json:"reason"`
	SentAtUnix   int64  `json:"sent_at_unix"`
	Signature    []byte `json:"signature,omitempty"`
}

type GroupChatText struct {
	Type         string `json:"type"`
	GroupID      string `json:"group_id"`
	Epoch        uint64 `json:"epoch"`
	MsgID        string `json:"msg_id"`
	SenderPeerID string `json:"sender_peer_id"`
	SenderSeq    uint64 `json:"sender_seq"`
	Ciphertext   []byte `json:"ciphertext"`
	SentAtUnix   int64  `json:"sent_at_unix"`
	Signature    []byte `json:"signature,omitempty"`
}

type GroupChatFile struct {
	Type         string `json:"type"`
	GroupID      string `json:"group_id"`
	Epoch        uint64 `json:"epoch"`
	MsgID        string `json:"msg_id"`
	SenderPeerID string `json:"sender_peer_id"`
	SenderSeq    uint64 `json:"sender_seq"`
	FileName     string `json:"file_name"`
	MIMEType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
	FileCID      string `json:"file_cid,omitempty"`
	Ciphertext   []byte `json:"ciphertext"`
	SentAtUnix   int64  `json:"sent_at_unix"`
	Signature    []byte `json:"signature,omitempty"`
}

type GroupDeliveryAck struct {
	Type        string `json:"type"`
	GroupID     string `json:"group_id"`
	MsgID       string `json:"msg_id"`
	FromPeerID  string `json:"from_peer_id"`
	ToPeerID    string `json:"to_peer_id"`
	AckedAtUnix int64  `json:"acked_at_unix"`
	Signature   []byte `json:"signature,omitempty"`
}

type GroupSyncCursor struct {
	GroupID      string    `json:"group_id"`
	PeerID       string    `json:"peer_id"`
	MaxSenderSeq uint64    `json:"max_sender_seq"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type GroupSyncRequest struct {
	Type          string            `json:"type"`
	GroupID       string            `json:"group_id"`
	LastEventSeq  uint64            `json:"last_event_seq"`
	SenderCursors map[string]uint64 `json:"sender_cursors"`
}

type GroupSyncResponse struct {
	Type         string                 `json:"type"`
	GroupID      string                 `json:"group_id"`
	Events       []GroupControlEnvelope `json:"events"`
	Messages     []GroupChatText        `json:"messages"`
	FileMessages []GroupChatFile        `json:"file_messages,omitempty"`
}
