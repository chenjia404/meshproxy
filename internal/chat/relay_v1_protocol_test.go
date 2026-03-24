package chat

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/net/mock"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/chenjia404/meshproxy/internal/chatrelay"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/tunnel"
)

func testBuildSignedRelayHandshake(t *testing.T, privA libp2pcrypto.PrivKey, sessionID, srcID, dstID string) *chatrelay.RelayHandshakeRequest {
	t.Helper()
	_, pubEph, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	req := &chatrelay.RelayHandshakeRequest{
		Version:     1,
		SessionID:   sessionID,
		SrcID:       srcID,
		DstID:       dstID,
		EphPub:      base64.StdEncoding.EncodeToString(pubEph),
		CipherSuite: chatrelay.CipherSuiteX25519Chacha,
		Timestamp:   time.Now().Unix(),
	}
	canon := chatrelay.CanonicalHandshake(req.Version, req.SessionID, req.SrcID, req.DstID, req.EphPub, req.CipherSuite, req.Timestamp)
	sig, err := chatrelay.SignBytes(privA, chatrelay.SignPrefixHandshake, canon)
	if err != nil {
		t.Fatal(err)
	}
	req.Signature = sig
	return req
}

// TestRelayV1HandshakeAsB_RejectsReplayWhileSessionValid：同一已签名 handshake 在 session 未过期时重放，B 端不得覆盖密钥。
func TestRelayV1HandshakeAsB_RejectsReplayWhileSessionValid(t *testing.T) {
	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	ma1, err := ma.NewMultiaddr("/ip6/::1/tcp/4242")
	if err != nil {
		t.Fatal(err)
	}
	ma2, err := ma.NewMultiaddr("/ip6/::1/tcp/4243")
	if err != nil {
		t.Fatal(err)
	}
	privA, _, err := libp2pcrypto.GenerateKeyPair(libp2pcrypto.Ed25519, 256)
	if err != nil {
		t.Fatal(err)
	}
	privB, _, err := libp2pcrypto.GenerateKeyPair(libp2pcrypto.Ed25519, 256)
	if err != nil {
		t.Fatal(err)
	}
	hA, err := mn.AddPeer(privA, ma1)
	if err != nil {
		t.Fatal(err)
	}
	hB, err := mn.AddPeer(privB, ma2)
	if err != nil {
		t.Fatal(err)
	}
	if err := mn.LinkAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := mn.ConnectPeers(hA.ID(), hB.ID()); err != nil {
		t.Fatal(err)
	}

	svc := &Service{
		ctx:       context.Background(),
		host:      hB,
		localPeer: hB.ID().String(),
		nodePriv:  privB,
	}
	sid := "test-session-replay"
	exp := time.Now().Unix() + 3600
	if !svc.relayV1BTryRegisterConnect(sid, hA.ID().String(), exp) {
		t.Fatal("register connect")
	}
	hs := testBuildSignedRelayHandshake(t, privA, sid, hA.ID().String(), hB.ID().String())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	handlerDone := make(chan struct{})
	hB.SetStreamHandler(chatrelay.ProtocolRelayHandshake, func(s network.Stream) {
		defer close(handlerDone)
		var req chatrelay.RelayHandshakeRequest
		if err := tunnel.ReadJSONFrame(s, &req); err != nil {
			return
		}
		svc.ServeRelayHandshakeAsB(s, &req)
	})

	str1, err := hA.NewStream(ctx, hB.ID(), chatrelay.ProtocolRelayHandshake)
	if err != nil {
		t.Fatal(err)
	}
	defer str1.Close()
	if err := tunnel.WriteJSONFrame(str1, hs); err != nil {
		t.Fatal(err)
	}
	var resp1 chatrelay.RelayHandshakeResponse
	if err := tunnel.ReadJSONFrame(str1, &resp1); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(resp1.Signature.Value) == "" {
		t.Fatal("expected signed handshake response")
	}
	if strings.TrimSpace(resp1.SessionID) != sid {
		t.Fatalf("first response session_id")
	}
	<-handlerDone

	tx1, rx1, ok := svc.relayV1BGetKeys(sid, hA.ID().String())
	if !ok || len(tx1) == 0 || len(rx1) == 0 {
		t.Fatal("expected keys after first handshake")
	}

	handlerDone2 := make(chan struct{})
	hB.SetStreamHandler(chatrelay.ProtocolRelayHandshake, func(s network.Stream) {
		defer close(handlerDone2)
		var req chatrelay.RelayHandshakeRequest
		if err := tunnel.ReadJSONFrame(s, &req); err != nil {
			return
		}
		svc.ServeRelayHandshakeAsB(s, &req)
	})

	str2, err := hA.NewStream(ctx, hB.ID(), chatrelay.ProtocolRelayHandshake)
	if err != nil {
		t.Fatal(err)
	}
	defer str2.Close()
	if err := tunnel.WriteJSONFrame(str2, hs); err != nil {
		t.Fatal(err)
	}
	var resp2 chatrelay.RelayHandshakeResponse
	err = tunnel.ReadJSONFrame(str2, &resp2)
	if err == nil {
		t.Fatal("replay should not write a second handshake response")
	}
	<-handlerDone2

	tx2, rx2, ok := svc.relayV1BGetKeys(sid, hA.ID().String())
	if !ok {
		t.Fatal("keys missing after replay attempt")
	}
	if string(tx1) != string(tx2) || string(rx1) != string(rx2) {
		t.Fatal("relay-v1 B keys must not be overwritten on handshake replay")
	}
}

// TestRelayV1PreferredRelayForDst_UsesBoundSession：已建立 relay-v1 会话时优先返回绑定的中继。
func TestRelayV1PreferredRelayForDst_UsesBoundSession(t *testing.T) {
	priv, _, err := libp2pcrypto.GenerateKeyPair(libp2pcrypto.Ed25519, 256)
	if err != nil {
		t.Fatal(err)
	}
	relayPID, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	dstPriv, _, err := libp2pcrypto.GenerateKeyPair(libp2pcrypto.Ed25519, 256)
	if err != nil {
		t.Fatal(err)
	}
	dstID, err := peer.IDFromPrivateKey(dstPriv)
	if err != nil {
		t.Fatal(err)
	}
	dstStr := dstID.String()

	s := &Service{
		ctx:             context.Background(),
		localPeer:       "local-self",
		relayV1Sessions: make(map[string]*relayV1PeerSession),
	}
	st := s.relayV1SessionFor(dstStr)
	st.mu.Lock()
	st.handshakeOK = true
	st.sessionID = "sid"
	st.expireAt = time.Now().Unix() + 100000
	st.relay = relayPID
	st.mu.Unlock()

	got, ok := s.relayV1PreferredRelayForDst(dstStr)
	if !ok || got != relayPID {
		t.Fatalf("preferred relay: got %v ok=%v want %s", got, ok, relayPID)
	}
}

// TestRelayV1DedupePrepend_PutsPreferredFirst：候选列表应将已绑定中继置于首位且不重复。
func TestRelayV1DedupePrepend_PutsPreferredFirst(t *testing.T) {
	priv1, _, _ := libp2pcrypto.GenerateKeyPair(libp2pcrypto.Ed25519, 256)
	priv2, _, _ := libp2pcrypto.GenerateKeyPair(libp2pcrypto.Ed25519, 256)
	p1, _ := peer.IDFromPrivateKey(priv1)
	p2, _ := peer.IDFromPrivateKey(priv2)
	base := []peer.ID{p2, p1}
	out := relayV1DedupePrepend(p1, base)
	if len(out) != 2 || out[0] != p1 || out[1] != p2 {
		t.Fatalf("got %v", out)
	}
}

// TestRelayV1VerifyHeartbeatPong_RejectsBadReplay：错误 ping_id / session 等应在验签前拒绝。
func TestRelayV1VerifyHeartbeatPong_RejectsBadReplay(t *testing.T) {
	priv, _, err := libp2pcrypto.GenerateKeyPair(libp2pcrypto.Ed25519, 256)
	if err != nil {
		t.Fatal(err)
	}
	relayPID, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	dstPriv, _, err := libp2pcrypto.GenerateKeyPair(libp2pcrypto.Ed25519, 256)
	if err != nil {
		t.Fatal(err)
	}
	dstPeerID, err := peer.IDFromPrivateKey(dstPriv)
	if err != nil {
		t.Fatal(err)
	}
	dstStr := dstPeerID.String()

	s := &Service{localPeer: "localB-node"}

	ping := &chatrelay.RelayHeartbeat{
		Version: 1, SessionID: "s1", SrcID: dstStr, DstID: "localB-node",
		RelayID: relayPID.String(), PingID: "ping-1", SentAt: time.Now().Unix(),
	}
	pongBadPing := &chatrelay.RelayHeartbeat{
		Version: 1, SessionID: "s1", SrcID: dstStr, DstID: "localB-node",
		RelayID: relayPID.String(), PingID: "ping-OTHER", SentAt: time.Now().Unix(),
	}
	if err := s.relayV1VerifyHeartbeatPong(ping, pongBadPing, relayPID, dstStr); err == nil {
		t.Fatal("wrong ping_id must fail")
	}

	pongBadSess := &chatrelay.RelayHeartbeat{
		Version: 1, SessionID: "OTHER", SrcID: dstStr, DstID: "localB-node",
		RelayID: relayPID.String(), PingID: "ping-1", SentAt: time.Now().Unix(),
	}
	if err := s.relayV1VerifyHeartbeatPong(ping, pongBadSess, relayPID, dstStr); err == nil {
		t.Fatal("wrong session_id must fail")
	}
}

// TestRelayV1BIngestRelayDataPacket_ReordersToContiguousDelivery：多 stream 下先到高序号再补低序号，应按 0,1,2… 连续交付。
func TestRelayV1BIngestRelayDataPacket_ReordersToContiguousDelivery(t *testing.T) {
	s := &Service{ctx: context.Background()}
	sid, src := "sid-reorder", "srcA"
	rx := make([]byte, 32)
	s.relayV1BMu.Lock()
	if s.relayV1BKeys == nil {
		s.relayV1BKeys = make(map[string]relayV1KeyEntry)
	}
	s.relayV1BKeys[relayV1BKey(sid, src)] = relayV1KeyEntry{tx: rx, rx: rx, lastDataSeq: ^uint64(0)}
	s.relayV1BMu.Unlock()

	if out := s.relayV1BIngestRelayDataPacket(sid, src, 1, []byte("one")); len(out) != 0 {
		t.Fatalf("seq 1 before 0 should buffer only, got %d", len(out))
	}
	out := s.relayV1BIngestRelayDataPacket(sid, src, 0, []byte("zero"))
	if len(out) != 2 || string(out[0]) != "zero" || string(out[1]) != "one" {
		t.Fatalf("want [zero one], got %#v", out)
	}
	if s.relayV1BIngestRelayDataPacket(sid, src, 1, []byte("dup")) != nil {
		t.Fatal("duplicate seq must drop")
	}
	out2 := s.relayV1BIngestRelayDataPacket(sid, src, 2, []byte("two"))
	if len(out2) != 1 || string(out2[0]) != "two" {
		t.Fatalf("want [two], got %#v", out2)
	}
}

// TestRelayV1BIngestRelayDataPacket_RejectsExcessiveGap：超前序号超过窗口则拒绝，避免内存被撑爆。
func TestRelayV1BIngestRelayDataPacket_RejectsExcessiveGap(t *testing.T) {
	s := &Service{ctx: context.Background()}
	sid, src := "sid-gap", "srcA"
	rx := make([]byte, 32)
	s.relayV1BMu.Lock()
	if s.relayV1BKeys == nil {
		s.relayV1BKeys = make(map[string]relayV1KeyEntry)
	}
	s.relayV1BKeys[relayV1BKey(sid, src)] = relayV1KeyEntry{tx: rx, rx: rx, lastDataSeq: 0}
	s.relayV1BMu.Unlock()

	// nextExpected=1，gap 需严格大于 relayV1BMaxDataReorderGap
	far := uint64(1) + relayV1BMaxDataReorderGap + 1
	if s.relayV1BIngestRelayDataPacket(sid, src, far, []byte("far")) != nil {
		t.Fatal("excessive gap should not buffer")
	}
}

// TestRelayV1BConnectValidLocked：握手持锁路径依赖 connect 未过期；过期、src 不一致或已 purge 必须为 false。
func TestRelayV1BConnectValidLocked(t *testing.T) {
	s := &Service{ctx: context.Background()}
	sid, src := "sid-ttl", "srcPeer"

	s.relayV1BMu.Lock()
	s.relayV1BConnect = map[string]relayV1BConnReg{
		sid: {srcID: src, expireAt: time.Now().Unix() + 3600},
	}
	if !s.relayV1BConnectValidLocked(sid, src) {
		t.Fatal("fresh connect should be valid")
	}
	s.relayV1BMu.Unlock()

	s.relayV1BMu.Lock()
	s.relayV1BConnect[sid] = relayV1BConnReg{srcID: src, expireAt: time.Now().Unix() - 1}
	if s.relayV1BConnectValidLocked(sid, src) {
		t.Fatal("expired connect must be invalid")
	}
	s.relayV1BMu.Unlock()

	s.relayV1BMu.Lock()
	s.relayV1BConnect = map[string]relayV1BConnReg{sid: {srcID: "other", expireAt: time.Now().Unix() + 3600}}
	if s.relayV1BConnectValidLocked(sid, src) {
		t.Fatal("src mismatch must be invalid")
	}
	s.relayV1BMu.Unlock()

	s.relayV1BMu.Lock()
	s.relayV1BConnect = map[string]relayV1BConnReg{sid: {srcID: src, expireAt: time.Now().Unix() + 3600}}
	s.relayV1BRemoveConnectLocked(sid)
	if s.relayV1BConnectValidLocked(sid, src) {
		t.Fatal("removed connect must be invalid")
	}
	s.relayV1BMu.Unlock()
}

// TestRelayV1BMaybePurgeRemovesExpiredConnect：握手中途若 connect 已过期，maybePurge 后校验失败（与 ServeRelayHandshakeAsB 第二段持锁一致）。
func TestRelayV1BMaybePurgeRemovesExpiredConnect(t *testing.T) {
	s := &Service{ctx: context.Background()}
	sid, src := "sid-purge", "srcP"
	s.relayV1BMu.Lock()
	s.relayV1BConnect = map[string]relayV1BConnReg{
		sid: {srcID: src, expireAt: time.Now().Unix() - 5},
	}
	s.relayV1BMaybePurgeLocked(time.Now())
	if s.relayV1BConnectValidLocked(sid, src) {
		t.Fatal("expired entry should be purged; connect must not validate")
	}
	s.relayV1BMu.Unlock()
}
