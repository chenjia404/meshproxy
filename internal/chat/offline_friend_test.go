package chat

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
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

func mustTestEd25519Key(t *testing.T) libp2pcrypto.PrivKey {
	t.Helper()
	priv, _, err := libp2pcrypto.GenerateEd25519Key(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func mustPeerIDStr(t *testing.T, priv libp2pcrypto.PrivKey) string {
	t.Helper()
	id, err := peer.IDFromPublicKey(priv.GetPublic())
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

// testOfflineFriendServiceWithKey 真实 SQLite + 本节点 libp2p 私钥（用于 ECIES 解密）。
func testOfflineFriendServiceWithKey(t *testing.T, localPriv libp2pcrypto.PrivKey) (*Service, func()) {
	t.Helper()
	localPeer := mustPeerIDStr(t, localPriv)
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
		nodePriv:  localPriv,
	}
	cleanup := func() { _ = st.Close() }
	return s, cleanup
}

func buildTestECIESOfflineFriendEnvelope(t *testing.T, senderPriv libp2pcrypto.PrivKey, senderID, recipientID, kind, msgID string, plain []byte) *OfflineMessageEnvelope {
	t.Helper()
	convID := deriveStableConversationID(senderID, recipientID)
	ttl := offlineDefaultTTLSec
	nonce := make([]byte, 12)
	if _, err := crand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	env := &OfflineMessageEnvelope{
		Version:        1,
		MsgID:          msgID,
		SenderID:       senderID,
		RecipientID:    recipientID,
		ConversationID: convID,
		CreatedAt:      1700000000,
		TTLSec:         &ttl,
		Cipher: OfflineCipherPayload{
			Algorithm:      OfflineFriendAlgoECIES,
			RecipientKeyID: encodeRecipientKeyID(0, kind),
			Nonce:          base64.StdEncoding.EncodeToString(nonce),
		},
	}
	aad := offlineFriendECIESAAD(env, offlineDefaultTTLSec)
	ct, err := encryptOfflineFriendECIES(recipientID, plain, aad, nonce)
	if err != nil {
		t.Fatal(err)
	}
	env.Cipher.Ciphertext = base64.StdEncoding.EncodeToString(ct)
	if err := signOfflineEnvelope(senderPriv, env, offlineDefaultTTLSec); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestProcessOfflineFriendPayload_InvalidEnvelope(t *testing.T) {
	t.Parallel()
	recvPriv := mustTestEd25519Key(t)
	recvID := mustPeerIDStr(t, recvPriv)
	s, cleanup := testOfflineFriendServiceWithKey(t, recvPriv)
	defer cleanup()

	t.Run("invalid base64 ciphertext", func(t *testing.T) {
		env := &OfflineMessageEnvelope{
			SenderID: mustPeerIDStr(t, mustTestEd25519Key(t)),
			Cipher: OfflineCipherPayload{
				Algorithm:      OfflineFriendAlgoECIES,
				RecipientKeyID: encodeRecipientKeyID(0, MessageTypeSessionRequest),
				Nonce:          base64.StdEncoding.EncodeToString(make([]byte, 12)),
				Ciphertext:     "@@@",
			},
		}
		_, err := s.processOfflineFriendPayload(env, 7)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("unsupported algorithm", func(t *testing.T) {
		snd := mustTestEd25519Key(t)
		req, _ := json.Marshal(SessionRequest{Type: MessageTypeSessionRequest, RequestID: "x", FromPeerID: mustPeerIDStr(t, snd), ToPeerID: recvID})
		env := buildTestECIESOfflineFriendEnvelope(t, snd, mustPeerIDStr(t, snd), recvID, MessageTypeSessionRequest, "m1", req)
		env.Cipher.Algorithm = "mesh-friend-plain-json"
		_, err := s.processOfflineFriendPayload(env, 1)
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("empty recipient_key_id", func(t *testing.T) {
		snd := mustTestEd25519Key(t)
		req, _ := json.Marshal(SessionRequest{Type: MessageTypeSessionRequest})
		env := buildTestECIESOfflineFriendEnvelope(t, snd, mustPeerIDStr(t, snd), recvID, MessageTypeSessionRequest, "m2", req)
		env.Cipher.RecipientKeyID = ""
		_, err := s.processOfflineFriendPayload(env, 1)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("unknown offline friend kind in recipient_key_id", func(t *testing.T) {
		sndPriv := mustTestEd25519Key(t)
		sndID := mustPeerIDStr(t, sndPriv)
		plain := []byte(`{}`)
		convID := deriveStableConversationID(sndID, recvID)
		ttl := offlineDefaultTTLSec
		nonce := make([]byte, 12)
		if _, err := crand.Read(nonce); err != nil {
			t.Fatal(err)
		}
		env := &OfflineMessageEnvelope{
			Version:        1,
			MsgID:          "m3",
			SenderID:       sndID,
			RecipientID:    recvID,
			ConversationID: convID,
			CreatedAt:      1700000000,
			TTLSec:         &ttl,
			Cipher: OfflineCipherPayload{
				Algorithm:      OfflineFriendAlgoECIES,
				RecipientKeyID: encodeRecipientKeyID(0, "chat_message"),
				Nonce:          base64.StdEncoding.EncodeToString(nonce),
			},
		}
		aad := offlineFriendECIESAAD(env, offlineDefaultTTLSec)
		ct, err := encryptOfflineFriendECIES(recvID, plain, aad, nonce)
		if err != nil {
			t.Fatal(err)
		}
		env.Cipher.Ciphertext = base64.StdEncoding.EncodeToString(ct)
		if err := signOfflineEnvelope(sndPriv, env, offlineDefaultTTLSec); err != nil {
			t.Fatal(err)
		}
		_, err = s.processOfflineFriendPayload(env, 2)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown offline friend kind") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("malformed json for session_request", func(t *testing.T) {
		snd := mustTestEd25519Key(t)
		env := buildTestECIESOfflineFriendEnvelope(t, snd, mustPeerIDStr(t, snd), recvID, MessageTypeSessionRequest, "m4", []byte(`{"type":`))
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
			snd := mustTestEd25519Key(t)
			env := buildTestECIESOfflineFriendEnvelope(t, snd, mustPeerIDStr(t, snd), recvID, tc.kind, "m5-"+tc.name, []byte(`{"type":`))
			_, err := s.processOfflineFriendPayload(env, 0)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}

	t.Run("invalid base64 in recipient_key_id", func(t *testing.T) {
		snd := mustTestEd25519Key(t)
		plain := []byte(`{}`)
		env := buildTestECIESOfflineFriendEnvelope(t, snd, mustPeerIDStr(t, snd), recvID, MessageTypeSessionRequest, "m6", plain)
		env.Cipher.RecipientKeyID = "not-valid-base64!!!"
		_, err := s.processOfflineFriendPayload(env, 0)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("counter in recipient_key_id does not change routing", func(t *testing.T) {
		sndPriv := mustTestEd25519Key(t)
		sndID := mustPeerIDStr(t, sndPriv)
		req := SessionRequest{
			Type:       MessageTypeSessionRequest,
			RequestID:  "req-counter-key",
			FromPeerID: sndID,
			ToPeerID:   recvID,
			SentAtUnix: 1700000000000,
		}
		b, _ := json.Marshal(req)
		env := buildTestECIESOfflineFriendEnvelope(t, sndPriv, sndID, recvID, MessageTypeSessionRequest, "m7", b)
		env.Cipher.RecipientKeyID = encodeRecipientKeyID(999, MessageTypeSessionRequest)
		nonce, _ := base64.StdEncoding.DecodeString(env.Cipher.Nonce)
		aad := offlineFriendECIESAAD(env, offlineDefaultTTLSec)
		ct, err := encryptOfflineFriendECIES(recvID, b, aad, nonce)
		if err != nil {
			t.Fatal(err)
		}
		env.Cipher.Ciphertext = base64.StdEncoding.EncodeToString(ct)
		if err := signOfflineEnvelope(sndPriv, env, offlineDefaultTTLSec); err != nil {
			t.Fatal(err)
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
		if got.FromPeerID != sndID {
			t.Fatalf("FromPeerID: got %q", got.FromPeerID)
		}
	})
}

func TestProcessOfflineFriendPayload_InnerSenderMismatch(t *testing.T) {
	t.Parallel()
	recvPriv := mustTestEd25519Key(t)
	recvID := mustPeerIDStr(t, recvPriv)
	sndPriv := mustTestEd25519Key(t)
	sndID := mustPeerIDStr(t, sndPriv)
	victimID := mustPeerIDStr(t, mustTestEd25519Key(t))
	s, cleanup := testOfflineFriendServiceWithKey(t, recvPriv)
	defer cleanup()

	basePlain := func(kind string, payload any) []byte {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	t.Run("session_request claims victim", func(t *testing.T) {
		t.Parallel()
		p := basePlain(MessageTypeSessionRequest, SessionRequest{
			Type:       MessageTypeSessionRequest,
			RequestID:  "r1",
			FromPeerID: victimID,
			ToPeerID:   recvID,
			SentAtUnix: 1,
		})
		env := buildTestECIESOfflineFriendEnvelope(t, sndPriv, sndID, recvID, MessageTypeSessionRequest, "r1", p)
		_, err := s.processOfflineFriendPayload(env, 1)
		if err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("want mismatch error, got %v", err)
		}
	})

	t.Run("session_accept claims victim", func(t *testing.T) {
		t.Parallel()
		p := basePlain(MessageTypeSessionAccept, SessionAccept{
			Type:             MessageTypeSessionAccept,
			RequestID:        "r1",
			ConversationID:   "c1",
			FromPeerID:       victimID,
			ToPeerID:         recvID,
			SentAtUnix:       1,
		})
		env := buildTestECIESOfflineFriendEnvelope(t, sndPriv, sndID, recvID, MessageTypeSessionAccept, "r1a", p)
		_, err := s.processOfflineFriendPayload(env, 1)
		if err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("want mismatch error, got %v", err)
		}
	})

	t.Run("session_reject claims victim", func(t *testing.T) {
		t.Parallel()
		p := basePlain(MessageTypeSessionReject, SessionReject{
			Type:       MessageTypeSessionReject,
			RequestID:  "r1",
			FromPeerID: victimID,
			ToPeerID:   recvID,
			SentAtUnix: 1,
		})
		env := buildTestECIESOfflineFriendEnvelope(t, sndPriv, sndID, recvID, MessageTypeSessionReject, "r1b", p)
		_, err := s.processOfflineFriendPayload(env, 1)
		if err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("want mismatch error, got %v", err)
		}
	})

	t.Run("session_accept_ack claims victim", func(t *testing.T) {
		t.Parallel()
		p := basePlain(MessageTypeSessionAcceptAck, SessionAcceptAck{
			Type:           MessageTypeSessionAcceptAck,
			RequestID:      "r1",
			ConversationID: "c1",
			FromPeerID:     victimID,
			ToPeerID:       recvID,
			SentAtUnix:     1,
		})
		env := buildTestECIESOfflineFriendEnvelope(t, sndPriv, sndID, recvID, MessageTypeSessionAcceptAck, "r1c", p)
		_, err := s.processOfflineFriendPayload(env, 1)
		if err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("want mismatch error, got %v", err)
		}
	})
}

func TestProcessOfflineFriendPayload_SessionRequest_Success(t *testing.T) {
	t.Parallel()
	recvPriv := mustTestEd25519Key(t)
	recvID := mustPeerIDStr(t, recvPriv)
	sndPriv := mustTestEd25519Key(t)
	sndID := mustPeerIDStr(t, sndPriv)
	s, cleanup := testOfflineFriendServiceWithKey(t, recvPriv)
	defer cleanup()

	reqID := "offline-friend-req-success-1"
	req := SessionRequest{
		Type:       MessageTypeSessionRequest,
		RequestID:  reqID,
		FromPeerID: sndID,
		ToPeerID:   recvID,
		Nickname:   "Bob",
		SentAtUnix: 1700000000123,
	}
	b, _ := json.Marshal(req)
	env := buildTestECIESOfflineFriendEnvelope(t, sndPriv, sndID, recvID, MessageTypeSessionRequest, "msg-ok", b)
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
	if stored.FromPeerID != sndID {
		t.Fatalf("FromPeerID: want %q got %q", sndID, stored.FromPeerID)
	}
	if stored.ToPeerID != recvID {
		t.Fatalf("ToPeerID: want %q got %q", recvID, stored.ToPeerID)
	}
}

func TestProcessOfflineFriendPayload_SessionRequest_WrongToPeerID_NoOp(t *testing.T) {
	t.Parallel()
	recvPriv := mustTestEd25519Key(t)
	recvID := mustPeerIDStr(t, recvPriv)
	sndPriv := mustTestEd25519Key(t)
	sndID := mustPeerIDStr(t, sndPriv)
	otherID := mustPeerIDStr(t, mustTestEd25519Key(t))
	s, cleanup := testOfflineFriendServiceWithKey(t, recvPriv)
	defer cleanup()

	reqID := "offline-friend-req-wrong-to"
	req := SessionRequest{
		Type:       MessageTypeSessionRequest,
		RequestID:  reqID,
		FromPeerID: sndID,
		ToPeerID:   otherID,
		SentAtUnix: 1,
	}
	b, _ := json.Marshal(req)
	env := buildTestECIESOfflineFriendEnvelope(t, sndPriv, sndID, recvID, MessageTypeSessionRequest, "msg-wto", b)
	if _, err := s.processOfflineFriendPayload(env, 5); err != nil {
		t.Fatal(err)
	}
	_, err := s.store.GetRequest(reqID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want sql.ErrNoRows, got %v", err)
	}
}

func TestBuildOfflineFriendEnvelope_InnerSenderMatchesOuter(t *testing.T) {
	t.Parallel()
	sndPriv := mustTestEd25519Key(t)
	sndID := mustPeerIDStr(t, sndPriv)
	recPriv := mustTestEd25519Key(t)
	recID := mustPeerIDStr(t, recPriv)
	s := &Service{
		localPeer: sndID,
		nodePriv:  sndPriv,
	}
	req := SessionRequest{
		Type:       MessageTypeSessionRequest,
		RequestID:  "build-req-1",
		FromPeerID: sndID,
		ToPeerID:   recID,
		SentAtUnix: 1,
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.buildOfflineFriendEnvelope(MessageTypeSessionRequest, recID, "msg-1", b)
	if err != nil {
		t.Fatal(err)
	}
	if env.SenderID != sndID {
		t.Fatalf("SenderID: want %q got %q", sndID, env.SenderID)
	}
	if env.Cipher.Algorithm != OfflineFriendAlgoECIES {
		t.Fatalf("algorithm: want %s", OfflineFriendAlgoECIES)
	}
	recvSvc := &Service{localPeer: recID, nodePriv: recPriv}
	payload, err := recvSvc.decryptOfflineFriendPayloadBytes(env)
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
