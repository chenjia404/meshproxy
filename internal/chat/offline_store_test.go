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

func createTestActiveGroup(t *testing.T, s *Service, groupID, senderPeerID string, groupKey []byte) {
	t.Helper()
	now := time.Now().UTC()
	group := Group{
		GroupID:          groupID,
		Title:            "test-group",
		ControllerPeerID: s.localPeer,
		CurrentEpoch:     1,
		State:            GroupStateActive,
		LastEventSeq:     1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	epoch := GroupEpoch{
		GroupID:            groupID,
		Epoch:              1,
		WrappedKeyForLocal: append([]byte(nil), groupKey...),
		CreatedAt:          now,
	}
	members := []GroupMember{
		{
			GroupID:     groupID,
			PeerID:      s.localPeer,
			Role:        GroupRoleController,
			State:       GroupMemberStateActive,
			JoinedEpoch: 1,
			UpdatedAt:   now,
		},
		{
			GroupID:     groupID,
			PeerID:      senderPeerID,
			Role:        GroupRoleMember,
			State:       GroupMemberStateActive,
			InvitedBy:   s.localPeer,
			JoinedEpoch: 1,
			UpdatedAt:   now,
		},
	}
	event := GroupEvent{
		EventID:      "event-create-" + groupID,
		GroupID:      groupID,
		EventSeq:     1,
		EventType:    GroupEventCreate,
		ActorPeerID:  s.localPeer,
		SignerPeerID: s.localPeer,
		PayloadJSON:  "{}",
		Signature:    []byte("sig"),
		CreatedAt:    now,
	}
	if _, err := s.store.CreateGroup(group, epoch, members, event); err != nil {
		t.Fatal(err)
	}
}

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

func TestProcessOneOfflineFetchItem_GroupTextFromOfflineStore(t *testing.T) {
	t.Parallel()

	recvPriv := mustTestEd25519Key(t)
	s, cleanup := testOfflineFriendServiceWithKey(t, recvPriv)
	defer cleanup()

	senderPriv := mustTestEd25519Key(t)
	senderPeerID := mustPeerIDStr(t, senderPriv)

	groupKey := []byte("0123456789abcdef0123456789abcdef")
	groupID := "group-offline-text"
	createTestActiveGroup(t, s, groupID, senderPeerID, groupKey)

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	maRecv, err := ma.NewMultiaddr("/ip6/::1/tcp/4262")
	if err != nil {
		t.Fatal(err)
	}
	maSender, err := ma.NewMultiaddr("/ip6/::1/tcp/4263")
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
	hSender.SetStreamHandler(p2p.ProtocolGroupMsg, func(str network.Stream) {
		defer str.Close()
		var env map[string]any
		_ = tunnel.ReadJSONFrame(str, &env)
	})
	s.host = hRecv

	senderSvc := &Service{host: hSender, localPeer: senderPeerID}
	plainText := "hello group offline store"
	wire := GroupChatText{
		Type:         MessageTypeGroupChatText,
		GroupID:      groupID,
		Epoch:        1,
		MsgID:        "group-offline-msg-1",
		SenderPeerID: senderPeerID,
		SenderSeq:    1,
		SentAtUnix:   time.Now().UTC().UnixMilli(),
	}
	aad := []byte(groupID + "\x00group_chat_text")
	wire.Ciphertext, err = protocol.AEADSeal(groupKey, protocol.BuildGroupChatNonce(groupID, senderPeerID, wire.SenderSeq), []byte(plainText), aad)
	if err != nil {
		t.Fatal(err)
	}
	if err := senderSvc.signGroupChatText(&wire); err != nil {
		t.Fatal(err)
	}

	env, err := s.buildOfflineGroupEnvelope(s.localPeer, wire)
	if err != nil {
		t.Fatal(err)
	}
	if env.Cipher.Algorithm != OfflineGroupWireAlgoV1 {
		t.Fatalf("want algorithm %q, got %q", OfflineGroupWireAlgoV1, env.Cipher.Algorithm)
	}
	if env.ConversationID != groupID {
		t.Fatalf("want group conversation_id %q, got %q", groupID, env.ConversationID)
	}
	if err := signOfflineEnvelope(senderPriv, env, offlineDefaultTTLSec); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(StoredMessageWire{
		StoreSeq:   11,
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
	if seq != 11 {
		t.Fatalf("want seq 11, got %d", seq)
	}

	msg, err := s.store.GetGroupMessage(wire.MsgID)
	if err != nil {
		t.Fatal(err)
	}
	if msg.GroupID != groupID {
		t.Fatalf("want group_id %q, got %q", groupID, msg.GroupID)
	}
	if msg.Plaintext != plainText {
		t.Fatalf("want plaintext %q, got %q", plainText, msg.Plaintext)
	}
	if msg.MsgType != MessageTypeGroupChatText {
		t.Fatalf("want msg_type %q, got %q", MessageTypeGroupChatText, msg.MsgType)
	}
}

func TestProcessOneOfflineFetchItem_GroupControlFromOfflineStore(t *testing.T) {
	t.Parallel()

	recvPriv := mustTestEd25519Key(t)
	s, cleanup := testOfflineFriendServiceWithKey(t, recvPriv)
	defer cleanup()

	senderPriv := mustTestEd25519Key(t)
	senderPeerID := mustPeerIDStr(t, senderPriv)
	mn := mocknet.New()
	defer func() { _ = mn.Close() }()
	maRecv, err := ma.NewMultiaddr("/ip6/::1/tcp/4272")
	if err != nil {
		t.Fatal(err)
	}
	hRecv, err := mn.AddPeer(recvPriv, maRecv)
	if err != nil {
		t.Fatal(err)
	}
	maSender, err := ma.NewMultiaddr("/ip6/::1/tcp/4273")
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
	senderSvc := &Service{host: hSender, localPeer: senderPeerID}
	s.host = hRecv

	groupID := "group-offline-control"
	groupKey := []byte("fedcba9876543210fedcba9876543210")
	now := time.Now().UTC()
	group := Group{
		GroupID:          groupID,
		Title:            "old-title",
		ControllerPeerID: senderPeerID,
		CurrentEpoch:     1,
		State:            GroupStateActive,
		LastEventSeq:     1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	epoch := GroupEpoch{
		GroupID:            groupID,
		Epoch:              1,
		WrappedKeyForLocal: append([]byte(nil), groupKey...),
		CreatedAt:          now,
	}
	members := []GroupMember{
		{
			GroupID:     groupID,
			PeerID:      senderPeerID,
			Role:        GroupRoleController,
			State:       GroupMemberStateActive,
			JoinedEpoch: 1,
			UpdatedAt:   now,
		},
		{
			GroupID:     groupID,
			PeerID:      s.localPeer,
			Role:        GroupRoleMember,
			State:       GroupMemberStateActive,
			InvitedBy:   senderPeerID,
			JoinedEpoch: 1,
			UpdatedAt:   now,
		},
	}
	createEvent := GroupEvent{
		EventID:      "event-create-" + groupID,
		GroupID:      groupID,
		EventSeq:     1,
		EventType:    GroupEventCreate,
		ActorPeerID:  senderPeerID,
		SignerPeerID: senderPeerID,
		PayloadJSON:  "{}",
		Signature:    []byte("sig"),
		CreatedAt:    now,
	}
	if _, err := s.store.CreateGroup(group, epoch, members, createEvent); err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(GroupTitleUpdatePayload{Title: "new-title"})
	if err != nil {
		t.Fatal(err)
	}
	event := GroupEvent{
		EventID:      "event-title-" + groupID,
		GroupID:      groupID,
		EventSeq:     2,
		EventType:    GroupEventTitleUpdate,
		ActorPeerID:  senderPeerID,
		SignerPeerID: senderPeerID,
		PayloadJSON:  string(payload),
		CreatedAt:    time.Now().UTC(),
	}
	if err := senderSvc.signGroupEvent(&event); err != nil {
		t.Fatal(err)
	}
	env, err := s.buildOfflineGroupEnvelope(s.localPeer, groupEnvelopeFromEvent(event))
	if err != nil {
		t.Fatal(err)
	}
	if err := signOfflineEnvelope(senderPriv, env, offlineDefaultTTLSec); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(StoredMessageWire{
		StoreSeq:   12,
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
	if seq != 12 {
		t.Fatalf("want seq 12, got %d", seq)
	}

	updated, err := s.store.GetGroup(groupID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "new-title" {
		t.Fatalf("want title %q, got %q", "new-title", updated.Title)
	}
	if updated.LastEventSeq != 2 {
		t.Fatalf("want last_event_seq 2, got %d", updated.LastEventSeq)
	}
}

func TestProcessOneOfflineFetchItem_RecoversCounterZeroDecryptFailureByResettingDirectState(t *testing.T) {
	t.Parallel()

	recvPriv := mustTestEd25519Key(t)
	s, cleanup := testOfflineFriendServiceWithKey(t, recvPriv)
	defer cleanup()

	senderPriv := mustTestEd25519Key(t)
	senderPeerID := mustPeerIDStr(t, senderPriv)

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	maRecv, err := ma.NewMultiaddr("/ip6/::1/tcp/4252")
	if err != nil {
		t.Fatal(err)
	}
	maSender, err := ma.NewMultiaddr("/ip6/::1/tcp/4253")
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

	recvProfile, recvChatPriv, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		t.Fatal(err)
	}
	senderChatPriv, senderChatPub, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	_, bogusChatPub, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	convID := deriveStableConversationID(s.localPeer, senderPeerID)
	if err := s.store.UpsertIncomingRequest(Request{
		RequestID:         "req-offline-reset",
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

	wrongSess, err := deriveSessionState(convID, s.localPeer, senderPeerID, recvChatPriv, base64.StdEncoding.EncodeToString(bogusChatPub))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CreateConversation(Conversation{
		ConversationID:    convID,
		PeerID:            senderPeerID,
		State:             ConversationStateActive,
		LastTransportMode: TransportModeDirect,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}, wrongSess); err != nil {
		t.Fatal(err)
	}

	senderSess, err := deriveSessionState(convID, senderPeerID, s.localPeer, senderChatPriv, recvProfile.ChatKexPub)
	if err != nil {
		t.Fatal(err)
	}
	nonce := protocol.BuildAEADNonce("fwd", 0)
	plaintext := []byte("hello after stale session reset")
	ciphertext, err := protocol.AEADSeal(senderSess.SendKey, nonce, plaintext, []byte(convID+"\x00"+MessageTypeChatText))
	if err != nil {
		t.Fatal(err)
	}

	ttl := offlineDefaultTTLSec
	env := &OfflineMessageEnvelope{
		Version:        1,
		MsgID:          "offline-msg-reset-1",
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
		StoreSeq:   11,
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
	if seq != 11 {
		t.Fatalf("want seq 11, got %d", seq)
	}

	msg, err := s.store.GetMessage("offline-msg-reset-1")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Plaintext != string(plaintext) {
		t.Fatalf("want plaintext %q, got %q", string(plaintext), msg.Plaintext)
	}
	sess, err := s.store.GetSessionState(convID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.RecvCounter != 1 {
		t.Fatalf("want recv counter 1, got %d", sess.RecvCounter)
	}
}

func TestProcessOneOfflineFetchItem_SkipsGapAndStoresCurrentMessage(t *testing.T) {
	t.Parallel()

	recvPriv := mustTestEd25519Key(t)
	s, cleanup := testOfflineFriendServiceWithKey(t, recvPriv)
	defer cleanup()

	senderPriv := mustTestEd25519Key(t)
	senderPeerID := mustPeerIDStr(t, senderPriv)

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	maRecv, err := ma.NewMultiaddr("/ip6/::1/tcp/4262")
	if err != nil {
		t.Fatal(err)
	}
	maSender, err := ma.NewMultiaddr("/ip6/::1/tcp/4263")
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

	recvProfile, recvChatPriv, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		t.Fatal(err)
	}
	senderChatPriv, senderChatPub, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	convID := deriveStableConversationID(s.localPeer, senderPeerID)
	if err := s.store.UpsertIncomingRequest(Request{
		RequestID:         "req-offline-gap-skip",
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

	recvSess, err := deriveSessionState(convID, s.localPeer, senderPeerID, recvChatPriv, base64.StdEncoding.EncodeToString(senderChatPub))
	if err != nil {
		t.Fatal(err)
	}
	recvSess.RecvCounter = 61
	if _, err := s.store.CreateConversation(Conversation{
		ConversationID:    convID,
		PeerID:            senderPeerID,
		State:             ConversationStateActive,
		LastTransportMode: TransportModeDirect,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}, recvSess); err != nil {
		t.Fatal(err)
	}

	senderSess, err := deriveSessionState(convID, senderPeerID, s.localPeer, senderChatPriv, recvProfile.ChatKexPub)
	if err != nil {
		t.Fatal(err)
	}
	counter := uint64(72)
	nonce := protocol.BuildAEADNonce("fwd", counter)
	plaintext := []byte("hello after skipped gap")
	ciphertext, err := protocol.AEADSeal(senderSess.SendKey, nonce, plaintext, []byte(convID+"\x00"+MessageTypeChatText))
	if err != nil {
		t.Fatal(err)
	}

	ttl := offlineDefaultTTLSec
	env := &OfflineMessageEnvelope{
		Version:        1,
		MsgID:          "offline-msg-gap-skip-1",
		SenderID:       senderPeerID,
		RecipientID:    s.localPeer,
		ConversationID: convID,
		CreatedAt:      time.Now().UTC().Unix(),
		TTLSec:         &ttl,
		Cipher: OfflineCipherPayload{
			Algorithm:      "chacha20-poly1305",
			RecipientKeyID: encodeRecipientKeyID(counter, MessageTypeChatText),
			Nonce:          base64.StdEncoding.EncodeToString(nonce),
			Ciphertext:     base64.StdEncoding.EncodeToString(ciphertext),
		},
	}
	if err := signOfflineEnvelope(senderPriv, env, offlineDefaultTTLSec); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(StoredMessageWire{
		StoreSeq:   21,
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
	if seq != 21 {
		t.Fatalf("want seq 21, got %d", seq)
	}

	msg, err := s.store.GetMessage("offline-msg-gap-skip-1")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Plaintext != string(plaintext) {
		t.Fatalf("want plaintext %q, got %q", string(plaintext), msg.Plaintext)
	}
	sess, err := s.store.GetSessionState(convID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.RecvCounter != counter+1 {
		t.Fatalf("want recv counter %d, got %d", counter+1, sess.RecvCounter)
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
