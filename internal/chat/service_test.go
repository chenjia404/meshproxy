package chat

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenjia404/meshproxy/internal/offlinestore"
	"github.com/chenjia404/meshproxy/internal/protocol"
)

func TestSendRequestDispatchesAsynchronously(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	st, err := NewStore(filepath.Join(tmp, "chat.db"), "12D3KooLOCAL")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if _, err := st.UpdateProfile("12D3KooLOCAL", "alice", "bio"); err != nil {
		t.Fatal(err)
	}

	offlineStarted := make(chan struct{}, 1)
	transportStarted := make(chan struct{}, 1)
	releaseOffline := make(chan struct{})
	releaseTransport := make(chan struct{})

	s := &Service{
		ctx:       context.Background(),
		store:     st,
		localPeer: "12D3KooLOCAL",
		eventsHub: newChatEventHub(),
		offlineStoreNodes: []offlinestore.OfflineStoreNode{
			{PeerID: "12D3KooSTORE"},
		},
		friendRequestStoreSubmitFn: func(kind, recipientPeer, msgID string, payload any, quiet bool) error {
			select {
			case offlineStarted <- struct{}{}:
			default:
			}
			<-releaseOffline
			return nil
		},
		friendRequestTransportFn: func(peerID string, v any) (bool, error) {
			select {
			case transportStarted <- struct{}{}:
			default:
			}
			<-releaseTransport
			return false, nil
		},
	}

	start := time.Now()
	req, err := s.SendRequest("12D3KooREMOTE", "  hello  ")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("SendRequest took too long: %s", time.Since(start))
	}
	if req.RequestID == "" {
		t.Fatal("request id is empty")
	}
	if req.State != RequestStatePending {
		t.Fatalf("state: want %s got %s", RequestStatePending, req.State)
	}

	select {
	case <-offlineStarted:
	case <-time.After(time.Second):
		t.Fatal("offline store send did not start")
	}

	select {
	case <-transportStarted:
		t.Fatal("transport started before offline send was released")
	default:
	}

	close(releaseOffline)

	select {
	case <-transportStarted:
	case <-time.After(time.Second):
		t.Fatal("transport send did not start after offline send completed")
	}

	close(releaseTransport)
}

func TestAcceptRequestDispatchesAsynchronously(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	st, err := NewStore(filepath.Join(tmp, "chat.db"), "12D3KooLOCAL")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if _, err := st.UpdateProfile("12D3KooLOCAL", "alice", "bio"); err != nil {
		t.Fatal(err)
	}
	_, remotePub, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	req := Request{
		RequestID:         "req-accept-async",
		FromPeerID:        "12D3KooREMOTE",
		ToPeerID:          "12D3KooLOCAL",
		State:             RequestStatePending,
		IntroText:         "hello",
		Nickname:          "remote",
		Bio:               "remote bio",
		RetentionMinutes:  0,
		RemoteChatKexPub:  base64.StdEncoding.EncodeToString(remotePub),
		LastTransportMode: TransportModeDirect,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := st.SaveOutgoingRequest(req); err != nil {
		t.Fatal(err)
	}

	offlineStarted := make(chan struct{}, 1)
	transportStarted := make(chan struct{}, 1)
	releaseOffline := make(chan struct{})
	releaseTransport := make(chan struct{})

	s := &Service{
		ctx:       context.Background(),
		store:     st,
		localPeer: "12D3KooLOCAL",
		eventsHub: newChatEventHub(),
		offlineStoreNodes: []offlinestore.OfflineStoreNode{
			{PeerID: "12D3KooSTORE"},
		},
		friendRequestStoreSubmitFn: func(kind, recipientPeer, msgID string, payload any, quiet bool) error {
			select {
			case offlineStarted <- struct{}{}:
			default:
			}
			<-releaseOffline
			return nil
		},
		friendRequestTransportFn: func(peerID string, v any) (bool, error) {
			select {
			case transportStarted <- struct{}{}:
			default:
			}
			<-releaseTransport
			return false, nil
		},
	}

	start := time.Now()
	conv, err := s.AcceptRequest(req.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("AcceptRequest took too long: %s", time.Since(start))
	}
	if conv.ConversationID == "" {
		t.Fatal("conversation id is empty")
	}
	if conv.PeerID != req.FromPeerID {
		t.Fatalf("peer id: want %s got %s", req.FromPeerID, conv.PeerID)
	}

	select {
	case <-offlineStarted:
	case <-time.After(time.Second):
		t.Fatal("offline store send did not start")
	}

	select {
	case <-transportStarted:
		t.Fatal("transport started before offline send was released")
	default:
	}

	close(releaseOffline)

	select {
	case <-transportStarted:
	case <-time.After(time.Second):
		t.Fatal("transport send did not start after offline send completed")
	}

	close(releaseTransport)
}
