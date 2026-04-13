package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenjia404/meshproxy/internal/offlinestore"
	"github.com/chenjia404/meshproxy/internal/protocol"
)

const asyncDispatchMaxLatency = 500 * time.Millisecond

type testMeshChatSigner struct {
	peerID string
}

func (s testMeshChatSigner) SignChallenge(challenge string) (string, string, string, error) {
	return "sig", "pub", s.peerID, nil
}

func waitForChatCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

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
	elapsed := time.Since(start)
	if elapsed > asyncDispatchMaxLatency {
		t.Fatalf("SendRequest took too long: %s", elapsed)
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
	elapsed := time.Since(start)
	if elapsed > asyncDispatchMaxLatency {
		t.Fatalf("AcceptRequest took too long: %s", elapsed)
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

func TestSendTextServerModeUsesMeshChatServer(t *testing.T) {
	t.Parallel()

	const (
		localPeer  = "12D3KooLOCAL"
		remotePeer = "12D3KooREMOTE"
		convID     = "conv-server-send"
		upConvID   = "up-conv-send"
		upMsgID    = "up-msg-send"
	)

	tmp := t.TempDir()
	st, err := NewStore(filepath.Join(tmp, "chat.db"), localPeer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	if _, err := st.CreateConversation(Conversation{
		ConversationID:     convID,
		PeerID:             remotePeer,
		State:              ConversationStateActive,
		LastTransportMode:  TransportModeDirect,
		CreatedAt:          now,
		UpdatedAt:          now,
		RetentionSyncState: "synced",
	}, sessionState{
		ConversationID: convID,
		PeerID:         remotePeer,
		SendKey:        make([]byte, protocol.AEADKeySize),
		RecvKey:        make([]byte, protocol.AEADKeySize),
	}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var postConversationCalls int
	var postMessageCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/challenge":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"challenge_id": "cid-1",
				"challenge":    "hello",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "tok-1"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/dm/conversations":
			mu.Lock()
			postConversationCalls++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(dmConversationView{
				ConversationID: upConvID,
				PeerID:         remotePeer,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/dm/conversations/"+upConvID+"/messages":
			mu.Lock()
			postMessageCalls++
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(dmMessageView{
				MessageID:       upMsgID,
				ConversationID:  upConvID,
				Seq:             1,
				ContentType:     "text",
				SenderPeerID:    localPeer,
				RecipientPeerID: remotePeer,
				ClientMsgID:     "client-msg-1",
				Status:          "sent",
				CreatedAt:       time.Now().UTC(),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := &Service{
		ctx:        context.Background(),
		store:      st,
		localPeer:  localPeer,
		eventsHub:  newChatEventHub(),
		meshChat:   newMeshChatHTTPClient(srv.URL, localPeer, testMeshChatSigner{peerID: localPeer}),
		serverMode: true,
	}

	msg, err := s.SendText(convID, "hello server mode")
	if err != nil {
		t.Fatal(err)
	}

	waitForChatCondition(t, 3*time.Second, func() bool {
		cur, err := st.GetMessage(msg.MsgID)
		if err != nil {
			return false
		}
		return cur.TransportMode == TransportModeServer &&
			cur.State == MessageStateSentToTransport &&
			cur.UpstreamMessageID == upMsgID
	})

	cur, err := st.GetMessage(msg.MsgID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.TransportMode != TransportModeServer {
		t.Fatalf("transport mode: want %s got %s", TransportModeServer, cur.TransportMode)
	}
	if cur.UpstreamMessageID != upMsgID {
		t.Fatalf("upstream message id: want %s got %s", upMsgID, cur.UpstreamMessageID)
	}

	conv, err := st.GetConversation(convID)
	if err != nil {
		t.Fatal(err)
	}
	if conv.UpstreamConversationID != upConvID {
		t.Fatalf("upstream conversation id: want %s got %s", upConvID, conv.UpstreamConversationID)
	}

	mu.Lock()
	defer mu.Unlock()
	if postConversationCalls != 1 {
		t.Fatalf("post conversation calls: want 1 got %d", postConversationCalls)
	}
	if postMessageCalls != 1 {
		t.Fatalf("post message calls: want 1 got %d", postMessageCalls)
	}
}

func TestSyncConversationServerModePullsFromMeshChat(t *testing.T) {
	t.Parallel()

	const (
		localPeer  = "12D3KooLOCAL"
		remotePeer = "12D3KooREMOTE"
		convID     = "conv-server-sync"
		upConvID   = "up-conv-sync"
		upMsgID    = "up-msg-sync"
	)

	tmp := t.TempDir()
	st, err := NewStore(filepath.Join(tmp, "chat.db"), localPeer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	if _, err := st.CreateConversation(Conversation{
		ConversationID:     convID,
		PeerID:             remotePeer,
		State:              ConversationStateActive,
		LastTransportMode:  TransportModeDirect,
		CreatedAt:          now,
		UpdatedAt:          now,
		RetentionSyncState: "synced",
	}, sessionState{
		ConversationID: convID,
		PeerID:         remotePeer,
		SendKey:        make([]byte, protocol.AEADKeySize),
		RecvKey:        make([]byte, protocol.AEADKeySize),
	}); err != nil {
		t.Fatal(err)
	}

	var getMessagesCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/challenge":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"challenge_id": "cid-1",
				"challenge":    "hello",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "tok-1"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/dm/conversations":
			_ = json.NewEncoder(w).Encode(dmConversationView{
				ConversationID: upConvID,
				PeerID:         remotePeer,
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/dm/conversations/"+upConvID+"/messages"):
			getMessagesCalls++
			_ = json.NewEncoder(w).Encode([]dmMessageView{
				{
					MessageID:       upMsgID,
					ConversationID:  upConvID,
					Seq:             1,
					ContentType:     "text",
					Payload:         map[string]string{"text": "hello from upstream"},
					SenderPeerID:    remotePeer,
					RecipientPeerID: localPeer,
					ClientMsgID:     "remote-client-msg-1",
					Status:          "sent",
					CreatedAt:       time.Now().UTC(),
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := &Service{
		ctx:        context.Background(),
		store:      st,
		localPeer:  localPeer,
		eventsHub:  newChatEventHub(),
		meshChat:   newMeshChatHTTPClient(srv.URL, localPeer, testMeshChatSigner{peerID: localPeer}),
		serverMode: true,
	}

	if err := s.SyncConversation(convID); err != nil {
		t.Fatal(err)
	}

	msgs, err := st.ListMessages(convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages len: want 1 got %d", len(msgs))
	}
	if msgs[0].TransportMode != TransportModeServer {
		t.Fatalf("transport mode: want %s got %s", TransportModeServer, msgs[0].TransportMode)
	}
	if msgs[0].UpstreamMessageID != upMsgID {
		t.Fatalf("upstream message id: want %s got %s", upMsgID, msgs[0].UpstreamMessageID)
	}
	if msgs[0].Plaintext != "hello from upstream" {
		t.Fatalf("plaintext: want %q got %q", "hello from upstream", msgs[0].Plaintext)
	}

	conv, err := st.GetConversation(convID)
	if err != nil {
		t.Fatal(err)
	}
	if conv.UpstreamConversationID != upConvID {
		t.Fatalf("upstream conversation id: want %s got %s", upConvID, conv.UpstreamConversationID)
	}
	if conv.LastUpstreamSyncSeq != 1 {
		t.Fatalf("last upstream sync seq: want 1 got %d", conv.LastUpstreamSyncSeq)
	}
	if getMessagesCalls == 0 {
		t.Fatal("expected upstream messages endpoint to be called")
	}
}
