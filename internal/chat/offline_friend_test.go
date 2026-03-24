package chat

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineFriendInnerSenderMustMatchEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     *OfflineMessageEnvelope
		inner   string
		wantErr bool
	}{
		{"nil envelope", nil, "a", true},
		{"exact match", &OfflineMessageEnvelope{SenderID: "12D3KooA"}, "12D3KooA", false},
		{"trim inner", &OfflineMessageEnvelope{SenderID: "12D3KooA"}, "  12D3KooA  ", false},
		{"trim sender", &OfflineMessageEnvelope{SenderID: "\t12D3KooA\n"}, "12D3KooA", false},
		{"both trim to empty", &OfflineMessageEnvelope{SenderID: "   "}, "  ", false},
		{"empty inner non-empty sender", &OfflineMessageEnvelope{SenderID: "12D3KooA"}, "", true},
		{"empty sender non-empty inner", &OfflineMessageEnvelope{SenderID: ""}, "12D3KooA", true},
		{"case sensitive mismatch", &OfflineMessageEnvelope{SenderID: "Ab"}, "ab", true},
		{"different peer ids", &OfflineMessageEnvelope{SenderID: "12D3KooAttacker"}, "12D3KooVictim", true},
		{"unicode id match", &OfflineMessageEnvelope{SenderID: "peer-α"}, "peer-α", false},
		{"unicode id mismatch", &OfflineMessageEnvelope{SenderID: "peer-α"}, "peer-β", true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := offlineFriendInnerSenderMustMatchEnv(tt.env, tt.inner)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// testOfflineFriendService 僅用於離線好友路徑測試：真實 SQLite + 精簡 Service（無 libp2p host）。
func testOfflineFriendService(t *testing.T) (*Service, func()) {
	t.Helper()
	localPeer := "12D3KooLOCALOFFLINETEST"
	tmp := t.TempDir()
	path := filepath.Join(tmp, "chat.db")
	st, err := NewStore(path, localPeer)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{
		ctx:       context.Background(),
		localPeer: localPeer,
		store:     st,
		eventsHub: newChatEventHub(),
	}
	cleanup := func() { _ = st.Close() }
	return s, cleanup
}

func mustEncodeOfflineFriendPayload(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func offlineFriendTestEnvelope(kind, senderID, b64Cipher string) *OfflineMessageEnvelope {
	return &OfflineMessageEnvelope{
		SenderID: senderID,
		Cipher: OfflineCipherPayload{
			RecipientKeyID: encodeRecipientKeyID(0, kind),
			Ciphertext:     b64Cipher,
		},
	}
}

func TestProcessOfflineFriendPayload_InvalidEnvelope(t *testing.T) {
	t.Parallel()
	s, cleanup := testOfflineFriendService(t)
	defer cleanup()

	t.Run("invalid base64 ciphertext", func(t *testing.T) {
		env := offlineFriendTestEnvelope(MessageTypeSessionRequest, "12D3KooA", "@@@not-valid-base64@@@")
		_, err := s.processOfflineFriendPayload(env, 7)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("empty recipient_key_id", func(t *testing.T) {
		env := &OfflineMessageEnvelope{
			SenderID: "12D3KooA",
			Cipher: OfflineCipherPayload{
				RecipientKeyID: "",
				Ciphertext:     mustEncodeOfflineFriendPayload(t, map[string]string{"type": MessageTypeSessionRequest}),
			},
		}
		_, err := s.processOfflineFriendPayload(env, 1)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("unknown offline friend kind in recipient_key_id", func(t *testing.T) {
		env := &OfflineMessageEnvelope{
			SenderID: "12D3KooA",
			Cipher: OfflineCipherPayload{
				RecipientKeyID: encodeRecipientKeyID(0, "chat_message"),
				Ciphertext:     mustEncodeOfflineFriendPayload(t, map[string]string{"foo": "bar"}),
			},
		}
		_, err := s.processOfflineFriendPayload(env, 2)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown offline friend kind") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("malformed json for session_request", func(t *testing.T) {
		env := offlineFriendTestEnvelope(MessageTypeSessionRequest, "12D3KooA", base64.StdEncoding.EncodeToString([]byte(`{"type":`)))
		_, err := s.processOfflineFriendPayload(env, 3)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	for _, tc := range []struct {
		name string
		kind string
	}{
		{"session_accept", MessageTypeSessionAccept},
		{"session_reject", MessageTypeSessionReject},
		{"session_accept_ack", MessageTypeSessionAcceptAck},
	} {
		tc := tc
		t.Run("malformed json for "+tc.name, func(t *testing.T) {
			env := offlineFriendTestEnvelope(tc.kind, "12D3KooA", base64.StdEncoding.EncodeToString([]byte(`{"type":`)))
			_, err := s.processOfflineFriendPayload(env, 0)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}

	t.Run("invalid base64 in recipient_key_id", func(t *testing.T) {
		env := &OfflineMessageEnvelope{
			SenderID: "12D3KooA",
			Cipher: OfflineCipherPayload{
				RecipientKeyID: "not-valid-base64!!!",
				Ciphertext:     mustEncodeOfflineFriendPayload(t, map[string]string{"type": MessageTypeSessionRequest}),
			},
		}
		_, err := s.processOfflineFriendPayload(env, 0)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("counter in recipient_key_id does not change routing", func(t *testing.T) {
		remote := "12D3KooREMOTESESSION"
		req := SessionRequest{
			Type:       MessageTypeSessionRequest,
			RequestID:  "req-counter-key",
			FromPeerID: remote,
			ToPeerID:   s.localPeer,
			SentAtUnix: 1700000000000,
		}
		env := &OfflineMessageEnvelope{
			SenderID: remote,
			Cipher: OfflineCipherPayload{
				RecipientKeyID: encodeRecipientKeyID(999, MessageTypeSessionRequest),
				Ciphertext:     mustEncodeOfflineFriendPayload(t, req),
			},
		}
		seq, err := s.processOfflineFriendPayload(env, 99)
		if err != nil {
			t.Fatal(err)
		}
		if seq != 99 {
			t.Fatalf("seq: want 99 got %d", seq)
		}
		got, err := s.store.GetRequest(req.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if got.FromPeerID != remote {
			t.Fatalf("FromPeerID: got %q", got.FromPeerID)
		}
	})
}

// TestProcessOfflineFriendPayload_InnerSenderMismatch 確保四種控制訊息在內層 from_peer_id 與外層 SenderID 不一致時一律失敗，且不依賴 store 狀態。
func TestProcessOfflineFriendPayload_InnerSenderMismatch(t *testing.T) {
	t.Parallel()
	s := &Service{localPeer: "12D3KooLOCAL"}
	attacker := "12D3KooAttacker"
	victim := "12D3KooVictim"
	baseEnv := func(kind string, payload any) *OfflineMessageEnvelope {
		return &OfflineMessageEnvelope{
			SenderID: attacker,
			Cipher: OfflineCipherPayload{
				RecipientKeyID: encodeRecipientKeyID(0, kind),
				Ciphertext:     mustEncodeOfflineFriendPayload(t, payload),
			},
		}
	}

	t.Run("session_request claims victim", func(t *testing.T) {
		t.Parallel()
		env := baseEnv(MessageTypeSessionRequest, SessionRequest{
			Type:       MessageTypeSessionRequest,
			RequestID:  "r1",
			FromPeerID: victim,
			ToPeerID:   s.localPeer,
			SentAtUnix: 1,
		})
		_, err := s.processOfflineFriendPayload(env, 1)
		if err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("want mismatch error, got %v", err)
		}
	})

	t.Run("session_accept claims victim", func(t *testing.T) {
		t.Parallel()
		env := baseEnv(MessageTypeSessionAccept, SessionAccept{
			Type:           MessageTypeSessionAccept,
			RequestID:      "r1",
			ConversationID: "c1",
			FromPeerID:     victim,
			ToPeerID:       s.localPeer,
			SentAtUnix:     1,
		})
		_, err := s.processOfflineFriendPayload(env, 1)
		if err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("want mismatch error, got %v", err)
		}
	})

	t.Run("session_reject claims victim", func(t *testing.T) {
		t.Parallel()
		env := baseEnv(MessageTypeSessionReject, SessionReject{
			Type:       MessageTypeSessionReject,
			RequestID:  "r1",
			FromPeerID: victim,
			ToPeerID:   s.localPeer,
			SentAtUnix: 1,
		})
		_, err := s.processOfflineFriendPayload(env, 1)
		if err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("want mismatch error, got %v", err)
		}
	})

	t.Run("session_accept_ack claims victim", func(t *testing.T) {
		t.Parallel()
		env := baseEnv(MessageTypeSessionAcceptAck, SessionAcceptAck{
			Type:           MessageTypeSessionAcceptAck,
			RequestID:      "r1",
			ConversationID: "c1",
			FromPeerID:     victim,
			ToPeerID:       s.localPeer,
			SentAtUnix:     1,
		})
		_, err := s.processOfflineFriendPayload(env, 1)
		if err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("want mismatch error, got %v", err)
		}
	})
}

func TestProcessOfflineFriendPayload_SessionRequest_Success(t *testing.T) {
	t.Parallel()
	s, cleanup := testOfflineFriendService(t)
	defer cleanup()

	remote := "12D3KooREMOTESENDER"
	reqID := "offline-friend-req-success-1"
	req := SessionRequest{
		Type:       MessageTypeSessionRequest,
		RequestID:  reqID,
		FromPeerID: remote,
		ToPeerID:   s.localPeer,
		Nickname:   "Bob",
		SentAtUnix: 1700000000123,
	}
	env := &OfflineMessageEnvelope{
		SenderID: remote,
		Cipher: OfflineCipherPayload{
			RecipientKeyID: encodeRecipientKeyID(0, MessageTypeSessionRequest),
			Ciphertext:     mustEncodeOfflineFriendPayload(t, req),
		},
	}
	storeSeq := uint64(42)
	outSeq, err := s.processOfflineFriendPayload(env, storeSeq)
	if err != nil {
		t.Fatal(err)
	}
	if outSeq != storeSeq {
		t.Fatalf("seq: want %d got %d", storeSeq, outSeq)
	}
	stored, err := s.store.GetRequest(reqID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != RequestStatePending {
		t.Fatalf("state: want %s got %s", RequestStatePending, stored.State)
	}
	if stored.FromPeerID != remote {
		t.Fatalf("FromPeerID: want %q got %q", remote, stored.FromPeerID)
	}
	if stored.ToPeerID != s.localPeer {
		t.Fatalf("ToPeerID: want %q got %q", s.localPeer, stored.ToPeerID)
	}
}

func TestProcessOfflineFriendPayload_SessionRequest_WrongToPeerID_NoOp(t *testing.T) {
	t.Parallel()
	s, cleanup := testOfflineFriendService(t)
	defer cleanup()

	remote := "12D3KooREMOTEOTHER"
	reqID := "offline-friend-req-wrong-to"
	req := SessionRequest{
		Type:       MessageTypeSessionRequest,
		RequestID:  reqID,
		FromPeerID: remote,
		ToPeerID:   "12D3KooSomeoneElse",
		SentAtUnix: 1,
	}
	env := &OfflineMessageEnvelope{
		SenderID: remote,
		Cipher: OfflineCipherPayload{
			RecipientKeyID: encodeRecipientKeyID(0, MessageTypeSessionRequest),
			Ciphertext:     mustEncodeOfflineFriendPayload(t, req),
		},
	}
	if _, err := s.processOfflineFriendPayload(env, 5); err != nil {
		t.Fatal(err)
	}
	_, err := s.store.GetRequest(reqID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want sql.ErrNoRows, got %v", err)
	}
}

// TestBuildOfflineFriendEnvelope_InnerSenderMatchesOuter 正常建包時 SenderID 與 JSON from_peer_id 同源，須通過 offlineFriendInnerSenderMustMatchEnv。
func TestBuildOfflineFriendEnvelope_InnerSenderMatchesOuter(t *testing.T) {
	t.Parallel()
	local := "12D3KooBUILDLOCAL"
	remote := "12D3KooBUILDREMOTE"
	s := &Service{localPeer: local}
	req := SessionRequest{
		Type:       MessageTypeSessionRequest,
		RequestID:  "build-req-1",
		FromPeerID: local,
		ToPeerID:   remote,
		SentAtUnix: 1,
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.buildOfflineFriendEnvelope(MessageTypeSessionRequest, remote, "msg-1", b)
	if err != nil {
		t.Fatal(err)
	}
	if env.SenderID != local {
		t.Fatalf("SenderID: want %q got %q", local, env.SenderID)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Cipher.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	var got SessionRequest
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if err := offlineFriendInnerSenderMustMatchEnv(env, got.FromPeerID); err != nil {
		t.Fatal(err)
	}
}
