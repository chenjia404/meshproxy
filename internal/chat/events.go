package chat

import (
	"sync"
	"time"
)

// ChatEvent is the payload sent to the web/chat websocket clients.
// For type "message", optional fields mirror chat.Message / GroupMessage so the UI can render
// without an extra HTTP fetch; other event kinds stay minimal.
// For type "message_state", the UI should merge MessageState / delivered_at / delivery_summary into the local row.
type ChatEvent struct {
	Type           string `json:"type"` // e.g. "message" | "message_state"
	Kind           string `json:"kind"` // "direct" | "group"
	ConversationID string `json:"conversation_id"`
	MsgID          string `json:"msg_id,omitempty"`
	MsgType        string `json:"msg_type,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	FromPeerID     string `json:"from_peer_id,omitempty"`
	ToPeerID       string `json:"to_peer_id,omitempty"`
	State          string `json:"state,omitempty"` // friend_request: pending/accepted/rejected
	AtUnixMillis   int64  `json:"at_unix_millis"`

	// Direct / group message body (optional; populated for inbound message notifications).
	Plaintext           string `json:"plaintext,omitempty"`
	FileName            string `json:"file_name,omitempty"`
	MIMEType            string `json:"mime_type,omitempty"`
	FileSize            int64  `json:"file_size,omitempty"`
	SenderPeerID        string `json:"sender_peer_id,omitempty"`
	ReceiverPeerID      string `json:"receiver_peer_id,omitempty"`
	Direction           string `json:"direction,omitempty"`
	Counter             uint64 `json:"counter,omitempty"`
	TransportMode       string `json:"transport_mode,omitempty"`
	MessageState        string `json:"message_state,omitempty"` // message row state (not friend_request State)
	CreatedAtUnixMillis int64  `json:"created_at_unix_millis,omitempty"`
	DeliveredAtUnixMillis int64 `json:"delivered_at_unix_millis,omitempty"` // direct outbound: peer ack time
	// Group-only (kind=="group")
	Epoch     uint64 `json:"epoch,omitempty"`
	SenderSeq uint64 `json:"sender_seq,omitempty"`
	// Group-only: per-recipient delivery rollup when kind=="group" and type=="message_state"
	DeliverySummary *GroupDeliverySummary `json:"delivery_summary,omitempty"`
}

type chatEventHub struct {
	mu     sync.Mutex
	nextID int
	subs   map[int]chan ChatEvent
}

func newChatEventHub() *chatEventHub {
	return &chatEventHub{
		nextID: 1,
		subs:   make(map[int]chan ChatEvent),
	}
}

func (h *chatEventHub) subscribe(buffer int) (<-chan ChatEvent, func()) {
	if h == nil {
		ch := make(chan ChatEvent)
		close(ch)
		return ch, func() {}
	}

	h.mu.Lock()
	id := h.nextID
	h.nextID++
	ch := make(chan ChatEvent, buffer)
	h.subs[id] = ch
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		if subCh, ok := h.subs[id]; ok {
			delete(h.subs, id)
			// Safe because publish holds h.mu while sending.
			close(subCh)
		}
		h.mu.Unlock()
	}

	return ch, unsubscribe
}

func (h *chatEventHub) publish(evt ChatEvent) {
	if h == nil {
		return
	}

	// Publish under lock so unsubscribe (delete+close) can't race with send.
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- evt:
		default:
			// Drop if the subscriber is slow.
		}
	}
}

func (s *Service) SubscribeChatEvents() (<-chan ChatEvent, func()) {
	if s == nil {
		ch := make(chan ChatEvent)
		close(ch)
		return ch, func() {}
	}
	return s.eventsHub.subscribe(64)
}

func (s *Service) publishChatEvent(evt ChatEvent) {
	if s == nil {
		return
	}
	s.eventsHub.publish(evt)
}

func newMessageEvent(kind, conversationID, msgID, msgType string) ChatEvent {
	return ChatEvent{
		Type:            "message",
		Kind:            kind,
		ConversationID: conversationID,
		MsgID:           msgID,
		MsgType:         msgType,
		AtUnixMillis:    time.Now().UTC().UnixMilli(),
	}
}

// chatEventDirectMessage builds a "message" event with the same core fields as chat.Message JSON.
func chatEventDirectMessage(m Message) ChatEvent {
	return ChatEvent{
		Type:                 "message",
		Kind:                 "direct",
		ConversationID:       m.ConversationID,
		MsgID:                m.MsgID,
		MsgType:              m.MsgType,
		Plaintext:            m.Plaintext,
		FileName:             m.FileName,
		MIMEType:             m.MIMEType,
		FileSize:             m.FileSize,
		SenderPeerID:         m.SenderPeerID,
		ReceiverPeerID:       m.ReceiverPeerID,
		Direction:            m.Direction,
		Counter:              m.Counter,
		TransportMode:        m.TransportMode,
		MessageState:         m.State,
		CreatedAtUnixMillis:  m.CreatedAt.UnixMilli(),
		AtUnixMillis:         time.Now().UTC().UnixMilli(),
	}
}

// chatEventGroupMessage builds a "message" event with text/file payload for group chats.
func chatEventGroupMessage(m GroupMessage) ChatEvent {
	return ChatEvent{
		Type:                 "message",
		Kind:                 "group",
		ConversationID:       m.GroupID,
		MsgID:                m.MsgID,
		MsgType:              m.MsgType,
		Plaintext:            m.Plaintext,
		FileName:             m.FileName,
		MIMEType:             m.MIMEType,
		FileSize:             m.FileSize,
		SenderPeerID:         m.SenderPeerID,
		Epoch:                m.Epoch,
		SenderSeq:            m.SenderSeq,
		MessageState:         m.State,
		CreatedAtUnixMillis:  m.CreatedAt.UnixMilli(),
		AtUnixMillis:         time.Now().UTC().UnixMilli(),
	}
}

func newFriendRequestEvent(state, requestID, fromPeerID, toPeerID, conversationID string) ChatEvent {
	return ChatEvent{
		Type:            "friend_request",
		Kind:            "direct",
		ConversationID: conversationID,
		RequestID:      requestID,
		FromPeerID:     fromPeerID,
		ToPeerID:       toPeerID,
		State:          state,
		AtUnixMillis:   time.Now().UTC().UnixMilli(),
	}
}

// newConversationDeletedEvent notifies WebSocket clients to refetch conversations/messages (local delete only).
func newConversationDeletedEvent(conversationID, peerID string) ChatEvent {
	return ChatEvent{
		Type:            "conversation_deleted",
		Kind:            "direct",
		ConversationID: conversationID,
		FromPeerID:      peerID,
		AtUnixMillis:    time.Now().UTC().UnixMilli(),
	}
}

func newContactDeletedEvent(peerID string) ChatEvent {
	return ChatEvent{
		Type:         "contact_deleted",
		Kind:         "direct",
		FromPeerID:   peerID,
		AtUnixMillis: time.Now().UTC().UnixMilli(),
	}
}

// newDirectMessageStateEvent notifies WS clients that a direct message row changed (sent / queued / delivered).
func newDirectMessageStateEvent(m Message) ChatEvent {
	evt := ChatEvent{
		Type:                 "message_state",
		Kind:                 "direct",
		ConversationID:       m.ConversationID,
		MsgID:                m.MsgID,
		MsgType:              m.MsgType,
		SenderPeerID:         m.SenderPeerID,
		ReceiverPeerID:       m.ReceiverPeerID,
		Direction:            m.Direction,
		Counter:              m.Counter,
		MessageState:         m.State,
		CreatedAtUnixMillis:  m.CreatedAt.UnixMilli(),
		AtUnixMillis:         time.Now().UTC().UnixMilli(),
	}
	if !m.DeliveredAt.IsZero() {
		evt.DeliveredAtUnixMillis = m.DeliveredAt.UnixMilli()
	}
	return evt
}

// newGroupMessageStateEvent notifies WS clients that a group message aggregate state changed.
func newGroupMessageStateEvent(m GroupMessage) ChatEvent {
	ds := m.DeliverySummary
	return ChatEvent{
		Type:            "message_state",
		Kind:            "group",
		ConversationID:  m.GroupID,
		MsgID:           m.MsgID,
		MsgType:         m.MsgType,
		SenderPeerID:    m.SenderPeerID,
		Epoch:           m.Epoch,
		SenderSeq:       m.SenderSeq,
		MessageState:    m.State,
		DeliverySummary: &ds,
		AtUnixMillis:    time.Now().UTC().UnixMilli(),
	}
}

