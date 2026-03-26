package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/chenjia404/meshproxy/internal/p2p"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/tunnel"
)

func TestPeekOfflineStoreSeq(t *testing.T) {
	t.Parallel()
	if got := peekOfflineStoreSeq([]byte(`{"store_seq":42,"message":{}}`)); got != 42 {
		t.Fatalf("want 42, got %d", got)
	}
	// 截断 JSON 仍应能提取序号
	if got := peekOfflineStoreSeq([]byte(`{"store_seq":99,"message":`)); got != 99 {
		t.Fatalf("want 99, got %d", got)
	}
	if got := peekOfflineStoreSeq([]byte(`not json`)); got != 0 {
		t.Fatalf("want 0, got %d", got)
	}
}

func TestIsPermanentOfflineFetchItemError(t *testing.T) {
	t.Parallel()

	if isPermanentOfflineFetchItemError(nil) {
		t.Fatal("nil error must not be permanent")
	}
	if isPermanentOfflineFetchItemError(errors.New("temporary")) {
		t.Fatal("plain error must not be permanent")
	}
	if !isPermanentOfflineFetchItemError(newPermanentOfflineFetchItemError(errors.New("bad envelope"))) {
		t.Fatal("wrapped permanent error must be detected")
	}
}

func TestProcessOneOfflineFetchItem_RepairsMissingSessionState(t *testing.T) {
	t.Parallel()

	recvPriv := mustTestEd25519Key(t)
	s, cleanup := testOfflineFriendServiceWithKey(t, recvPriv)
	defer cleanup()

	senderPriv := mustTestEd25519Key(t)
	senderPeerID := mustPeerIDStr(t, senderPriv)

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	maRecv, err := ma.NewMultiaddr("/ip6/::1/tcp/4242")
	if err != nil {
		t.Fatal(err)
	}
	maSender, err := ma.NewMultiaddr("/ip6/::1/tcp/4243")
	if err != nil {
		t.Fatal(err)
	}
	hRecv, err := mn.AddPeer(recvPriv, maRecv)
	if err != nil {
		t.Fatal(err)
	}
	hSender, err := mn.AddPeer(senderPriv, maSender)
	if err != nil {
		t.Fatal(err)
	}
	if err := mn.LinkAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := mn.ConnectPeers(hRecv.ID(), hSender.ID()); err != nil {
		t.Fatal(err)
	}
	hSender.SetStreamHandler(p2p.ProtocolChatAck, func(str network.Stream) {
		defer str.Close()
		var ack DeliveryAck
		_ = tunnel.ReadJSONFrame(str, &ack)
	})
	s.host = hRecv

	recvProfile, _, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		t.Fatal(err)
	}
	senderChatPriv, senderChatPub, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	convID := deriveStableConversationID(s.localPeer, senderPeerID)
	if err := s.store.UpsertIncomingRequest(Request{
		RequestID:         "req-offline-repair",
		FromPeerID:        senderPeerID,
		ToPeerID:          s.localPeer,
		State:             RequestStateAccepted,
		RemoteChatKexPub:  base64.StdEncoding.EncodeToString(senderChatPub),
		ConversationID:    convID,
		LastTransportMode: TransportModeDirect,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	senderSess, err := deriveSessionState(convID, senderPeerID, s.localPeer, senderChatPriv, recvProfile.ChatKexPub)
	if err != nil {
		t.Fatal(err)
	}
	nonce := protocol.BuildAEADNonce("fwd", 0)
	plaintext := []byte("hello from offline store")
	ciphertext, err := protocol.AEADSeal(senderSess.SendKey, nonce, plaintext, []byte(convID+"\x00"+MessageTypeChatText))
	if err != nil {
		t.Fatal(err)
	}

	ttl := offlineDefaultTTLSec
	env := &OfflineMessageEnvelope{
		Version:        1,
		MsgID:          "offline-msg-1",
		SenderID:       senderPeerID,
		RecipientID:    s.localPeer,
		ConversationID: convID,
		CreatedAt:      time.Now().UTC().Unix(),
		TTLSec:         &ttl,
		Cipher: OfflineCipherPayload{
			Algorithm:      "chacha20-poly1305",
			RecipientKeyID: encodeRecipientKeyID(0, MessageTypeChatText),
			Nonce:          base64.StdEncoding.EncodeToString(nonce),
			Ciphertext:     base64.StdEncoding.EncodeToString(ciphertext),
		},
	}
	if err := signOfflineEnvelope(senderPriv, env, offlineDefaultTTLSec); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(StoredMessageWire{
		StoreSeq:   7,
		ReceivedAt: time.Now().UTC().Unix(),
		ExpireAt:   time.Now().UTC().Add(time.Hour).Unix(),
		Message:    env,
	})
	if err != nil {
		t.Fatal(err)
	}

	seq, err := s.processOneOfflineFetchItem(raw)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 7 {
		t.Fatalf("want seq 7, got %d", seq)
	}

	if _, err := s.store.GetConversation(convID); err != nil {
		t.Fatalf("conversation should be repaired: %v", err)
	}
	if _, err := s.store.GetSessionState(convID); err != nil {
		t.Fatalf("session state should be repaired: %v", err)
	}
	msg, err := s.store.GetMessage("offline-msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Plaintext != string(plaintext) {
		t.Fatalf("want plaintext %q, got %q", string(plaintext), msg.Plaintext)
	}
	if msg.TransportMode != TransportModeOfflineStore {
		t.Fatalf("want transport mode %q, got %q", TransportModeOfflineStore, msg.TransportMode)
	}
}

func TestProcessOneOfflineFetchItem_MalformedJSONIsPermanent(t *testing.T) {
	t.Parallel()

	s := &Service{ctx: context.Background(), localPeer: "12D3KooWLocal"}
	seq, err := s.processOneOfflineFetchItem([]byte(`{"store_seq":9,"message":`))
	if seq != 9 {
		t.Fatalf("want seq 9, got %d", seq)
	}
	if err == nil {
		t.Fatal("expected error")
	}
	if !isPermanentOfflineFetchItemError(err) {
		t.Fatal("malformed JSON should be classified as permanent")
	}
}
