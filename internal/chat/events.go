package chat

import (
	"sync"
	"time"
)

// ChatEvent is the payload sent to the web/chat websocket clients.
// It is intentionally minimal: the frontend will re-fetch message lists and update UI.
type ChatEvent struct {
	Type           string    `json:"type"` // e.g. "message"
	Kind           string    `json:"kind"` // "direct" | "group"
	ConversationID string    `json:"conversation_id"`
	MsgID          string    `json:"msg_id,omitempty"`
	MsgType        string    `json:"msg_type,omitempty"`
	RequestID     string    `json:"request_id,omitempty"`
	FromPeerID    string    `json:"from_peer_id,omitempty"`
	ToPeerID      string    `json:"to_peer_id,omitempty"`
	State         string    `json:"state,omitempty"` // e.g. pending/accepted/rejected
	AtUnixMillis   int64     `json:"at_unix_millis"`
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
		AtUnixMillis:   time.Now().UTC().UnixMilli(),
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

