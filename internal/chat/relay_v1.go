package chat

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/chenjia404/meshproxy/internal/chatrelay"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/safe"
	"github.com/chenjia404/meshproxy/internal/tunnel"
)

type relayV1BConnReg struct {
	srcID    string
	expireAt int64
}

type relayV1KeyEntry struct {
	tx, rx      []byte
	lastDataSeq uint64            // 已連續交付的最大 packet_seq；首包前為 ^uint64(0)（下一期望為 0）
	pending     map[uint64][]byte // 已解密明文，等待缺號到齊後再交業務層（多 stream 亂序）
}

func relayV1BKey(sessionID, srcID string) string {
	return sessionID + "\x00" + srcID
}

// B 端入站表防資源耗盡（與中繼側 ChatRelayV1Table 對齊的常數）。
const (
	relayV1BMaxSessionsPerSrc = 64
	relayV1BMaxSessionsTotal  = 8192
	relayV1BPurgeInterval     = 2 * time.Second
	relayV1BMinConnectGap     = 200 * time.Millisecond
	relayV1BMinHeartbeatGap   = 400 * time.Millisecond

	// 多 stream 轉發亂序：允許的超前序號缺口與 pending 上限（防內存放大）。
	relayV1BMaxDataReorderGap   = uint64(64)
	relayV1BMaxDataPendingSlots = 64
)

func relayV1BRemoveStringFromSlice(s []string, x string) []string {
	out := s[:0]
	for _, y := range s {
		if y != x {
			out = append(out, y)
		}
	}
	return out
}

func (s *Service) relayV1BMaybePurgeLocked(now time.Time) {
	nowUnix := now.Unix()
	heavy := s.relayV1BLastPurge.IsZero() || now.Sub(s.relayV1BLastPurge) >= relayV1BPurgeInterval ||
		len(s.relayV1BConnect) > relayV1BMaxSessionsTotal
	if !heavy {
		return
	}
	s.relayV1BLastPurge = now
	for sid, r := range s.relayV1BConnect {
		if nowUnix > r.expireAt {
			s.relayV1BRemoveConnectLocked(sid)
		}
	}
	for len(s.relayV1BConnect) > relayV1BMaxSessionsTotal {
		n := len(s.relayV1BConnect)
		s.relayV1BEvictOldestLocked()
		if len(s.relayV1BConnect) >= n {
			break
		}
	}
}

func (s *Service) relayV1BRemoveConnectLocked(sessionID string) {
	r, ok := s.relayV1BConnect[sessionID]
	if !ok {
		return
	}
	delete(s.relayV1BConnect, sessionID)
	if s.relayV1BBySrc != nil {
		s.relayV1BBySrc[r.srcID] = relayV1BRemoveStringFromSlice(s.relayV1BBySrc[r.srcID], sessionID)
	}
	if s.relayV1BKeys != nil {
		delete(s.relayV1BKeys, relayV1BKey(sessionID, r.srcID))
	}
}

func (s *Service) relayV1BEvictOldestLocked() {
	var victim string
	var minExp int64 = 1<<62 - 1
	for sid, r := range s.relayV1BConnect {
		if r.expireAt < minExp {
			minExp = r.expireAt
			victim = sid
		}
	}
	if victim != "" {
		s.relayV1BRemoveConnectLocked(victim)
	}
}

// relayV1BConnectValidLocked 校驗 relayV1BConnect 中該 session 仍存在、src 一致且未過期（須已持 relayV1BMu；可在 maybePurge 後調用）。
func (s *Service) relayV1BConnectValidLocked(sessionID, srcID string) bool {
	if s.relayV1BConnect == nil {
		return false
	}
	r, ok := s.relayV1BConnect[sessionID]
	if !ok || r.srcID != srcID {
		return false
	}
	return time.Now().Unix() <= r.expireAt
}

func (s *Service) relayV1BEnforcePerSrcCapLocked(srcID string) {
	for s.relayV1BBySrc != nil && len(s.relayV1BBySrc[srcID]) >= relayV1BMaxSessionsPerSrc {
		ids := s.relayV1BBySrc[srcID]
		if len(ids) == 0 {
			break
		}
		s.relayV1BRemoveConnectLocked(ids[0])
	}
}

// relayV1BTryRegisterConnect 登記 connect；若建鏈過頻則拒絕（調用方不應再發 Accept）。
func (s *Service) relayV1BTryRegisterConnect(sessionID, srcID string, expireAt int64) bool {
	if sessionID == "" || srcID == "" {
		return false
	}
	s.relayV1BMu.Lock()
	defer s.relayV1BMu.Unlock()
	now := time.Now()
	s.relayV1BMaybePurgeLocked(now)
	if s.relayV1BConnect == nil {
		s.relayV1BConnect = make(map[string]relayV1BConnReg)
	}
	if _, dup := s.relayV1BConnect[sessionID]; dup {
		return true
	}
	if s.relayV1BLastConnectAt == nil {
		s.relayV1BLastConnectAt = make(map[string]time.Time)
	}
	if last, ok := s.relayV1BLastConnectAt[srcID]; ok && now.Sub(last) < relayV1BMinConnectGap {
		return false
	}
	s.relayV1BLastConnectAt[srcID] = now
	s.relayV1BEnforcePerSrcCapLocked(srcID)
	for len(s.relayV1BConnect) > relayV1BMaxSessionsTotal {
		n := len(s.relayV1BConnect)
		s.relayV1BEvictOldestLocked()
		if len(s.relayV1BConnect) >= n {
			break
		}
	}
	s.relayV1BConnect[sessionID] = relayV1BConnReg{srcID: srcID, expireAt: expireAt}
	if s.relayV1BBySrc == nil {
		s.relayV1BBySrc = make(map[string][]string)
	}
	s.relayV1BBySrc[srcID] = append(s.relayV1BBySrc[srcID], sessionID)
	return true
}

// relay v1 每目標 peer 的會話（文檔八：復用 session）。
type relayV1PeerSession struct {
	mu sync.Mutex

	relay       peer.ID
	sessionID   string
	expireAt    int64
	txKey       []byte
	rxKey       []byte
	packetSeq   uint64
	handshakeOK bool

	lastUsedUnix int64 // 最近一次成功建立/發送/首選命中（供 map 超限時淘汰最久未用）

	hbFail int
	stopHB chan struct{}
}

// relayV1PeerSessionStopHeartbeatLocked 关闭心跳 goroutine（须已持 st.mu）。
func relayV1PeerSessionStopHeartbeatLocked(st *relayV1PeerSession) {
	if st == nil || st.stopHB == nil {
		return
	}
	close(st.stopHB)
	st.stopHB = nil
}

// A 端 relayV1Sessions 惰性回收：過期項 + 總量超上限時按 lastUsedUnix 淘汰。
const (
	relayV1ASessionPurgeInterval = 2 * time.Second
	relayV1ASessionMaxEntries    = 4096
)

func (s *Service) relayV1MaybePurgeStaleRelaySessionsLocked() {
	if s.relayV1Sessions == nil {
		return
	}
	now := time.Now()
	heavy := s.relayV1SessionsLastPurge.IsZero() || now.Sub(s.relayV1SessionsLastPurge) >= relayV1ASessionPurgeInterval ||
		len(s.relayV1Sessions) > relayV1ASessionMaxEntries
	if !heavy {
		return
	}
	s.relayV1SessionsLastPurge = now
	nowUnix := now.Unix()
	var rm []string
	for dst, st := range s.relayV1Sessions {
		if st == nil {
			rm = append(rm, dst)
			continue
		}
		st.mu.Lock()
		expired := st.handshakeOK && nowUnix >= st.expireAt-5
		st.mu.Unlock()
		if expired {
			rm = append(rm, dst)
		}
	}
	for _, dst := range rm {
		st := s.relayV1Sessions[dst]
		if st != nil {
			st.mu.Lock()
			relayV1PeerSessionStopHeartbeatLocked(st)
			st.mu.Unlock()
		}
		delete(s.relayV1Sessions, dst)
	}
	for len(s.relayV1Sessions) > relayV1ASessionMaxEntries {
		var worstDst string
		var worstScore int64 = 1<<62 - 1
		for dst, st := range s.relayV1Sessions {
			if st == nil {
				worstDst = dst
				break
			}
			st.mu.Lock()
			t := st.lastUsedUnix
			st.mu.Unlock()
			if t < worstScore {
				worstScore = t
				worstDst = dst
			}
		}
		if worstDst == "" {
			break
		}
		st := s.relayV1Sessions[worstDst]
		if st != nil {
			st.mu.Lock()
			relayV1PeerSessionStopHeartbeatLocked(st)
			st.mu.Unlock()
		}
		delete(s.relayV1Sessions, worstDst)
	}
}

func (s *Service) relayV1SessionFor(dstPeer string) *relayV1PeerSession {
	s.relayV1SessionsMu.Lock()
	defer s.relayV1SessionsMu.Unlock()
	s.relayV1MaybePurgeStaleRelaySessionsLocked()
	if s.relayV1Sessions == nil {
		s.relayV1Sessions = make(map[string]*relayV1PeerSession)
	}
	st, ok := s.relayV1Sessions[dstPeer]
	if !ok {
		st = &relayV1PeerSession{lastUsedUnix: time.Now().Unix()}
		s.relayV1Sessions[dstPeer] = st
	}
	return st
}

func (s *Service) sendViaRelayV1(relayPID peer.ID, dstPeerID string, payload []byte) error {
	log.Printf("[chat] relayV1 send begin relay=%s dst=%s payload_len=%d", relayPID.String(), dstPeerID, len(payload))
	st := s.relayV1SessionFor(dstPeerID)
	st.mu.Lock()
	defer st.mu.Unlock()

	now := time.Now().Unix()
	needEstablish := !st.handshakeOK || st.sessionID == "" || now >= st.expireAt-5
	// 會話與當初中繼 R 綁定；候選順序變化或 failover 換了 relay 時必須重建，否則新 R 無此 session，數據幀會被丟棄。
	if st.handshakeOK && st.relay != relayPID {
		needEstablish = true
	}
	if needEstablish {
		log.Printf("[chat] relayV1 establish needed relay=%s dst=%s handshake_ok=%t session_id=%s expire_at=%d",
			relayPID.String(), dstPeerID, st.handshakeOK, st.sessionID, st.expireAt)
		if err := s.relayV1Establish(s.ctx, relayPID, dstPeerID, st); err != nil {
			log.Printf("[chat] relayV1 establish failed relay=%s dst=%s err=%v", relayPID.String(), dstPeerID, err)
			return err
		}
		log.Printf("[chat] relayV1 establish ok relay=%s dst=%s session_id=%s", relayPID.String(), dstPeerID, st.sessionID)
	}

	nonce, ct, err := chatrelay.SealDataFrame(st.txKey, st.sessionID, st.packetSeq, payload)
	if err != nil {
		return err
	}
	st.packetSeq++

	frame := chatrelay.RelayDataFrame{
		Version:   1,
		SessionID: st.sessionID,
		SrcID:     s.localPeer,
		DstID:     dstPeerID,
		PacketSeq: st.packetSeq - 1,
		Cipher: chatrelay.RelayDataCipher{
			Algorithm:  "chacha20-poly1305",
			Nonce:      base64.StdEncoding.EncodeToString(nonce),
			Ciphertext: base64.StdEncoding.EncodeToString(ct),
		},
	}

	cctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	str, err := s.host.NewStream(cctx, relayPID, chatrelay.ProtocolRelayData)
	if err != nil {
		log.Printf("[chat] relayV1 data stream failed relay=%s dst=%s err=%v", relayPID.String(), dstPeerID, err)
		return err
	}
	defer str.Close()
	_ = str.SetDeadline(time.Now().Add(45 * time.Second))
	log.Printf("[chat] relayV1 data stream-open relay=%s dst=%s session_id=%s packet_seq=%d",
		relayPID.String(), dstPeerID, st.sessionID, frame.PacketSeq)
	if err := tunnel.WriteJSONFrame(str, &frame); err != nil {
		log.Printf("[chat] relayV1 data write failed relay=%s dst=%s session_id=%s packet_seq=%d err=%v",
			relayPID.String(), dstPeerID, st.sessionID, frame.PacketSeq, err)
		return err
	}
	log.Printf("[chat] relayV1 data write-ok relay=%s dst=%s session_id=%s packet_seq=%d",
		relayPID.String(), dstPeerID, st.sessionID, frame.PacketSeq)
	st.lastUsedUnix = time.Now().Unix()
	s.relayV1MaybeStartHeartbeat(relayPID, dstPeerID, st)
	return nil
}

// relay v1 建鏈響應與本地請求對齊的容差（秒）：過期時間相對預期 ts+TTL、握手時間戳相對當前時間。
const (
	relayV1EstablishExpireSkewSec      int64 = 300
	relayV1EstablishHsTimestampSkewSec int64 = 600
)

func relayV1Trim(s string) string { return strings.TrimSpace(s) }

func relayV1ValidateConnectResponse(resp *chatrelay.RelayConnectResponse, localPeer, dstPeerID string, relayPID peer.ID, sessionID string, reqTs int64, ttlSec int) error {
	if resp == nil {
		return fmt.Errorf("%s: nil connect response", chatrelay.ErrHandshakeInvalid)
	}
	if resp.Version != 1 {
		return fmt.Errorf("%s: connect response version mismatch", chatrelay.ErrHandshakeInvalid)
	}
	if !resp.Accepted {
		return fmt.Errorf("%s: connect not accepted", chatrelay.ErrHandshakeInvalid)
	}
	if relayV1Trim(resp.SessionID) != relayV1Trim(sessionID) {
		return fmt.Errorf("%s: connect response session_id mismatch", chatrelay.ErrHandshakeInvalid)
	}
	if relayV1Trim(resp.SrcID) != relayV1Trim(dstPeerID) {
		return fmt.Errorf("%s: connect response src_id mismatch", chatrelay.ErrHandshakeInvalid)
	}
	if relayV1Trim(resp.DstID) != relayV1Trim(localPeer) {
		return fmt.Errorf("%s: connect response dst_id mismatch", chatrelay.ErrHandshakeInvalid)
	}
	if relayV1Trim(resp.RelayID) != relayV1Trim(relayPID.String()) {
		return fmt.Errorf("%s: connect response relay_id mismatch", chatrelay.ErrHandshakeInvalid)
	}
	exp := reqTs + int64(ttlSec)
	diff := resp.ExpireAt - exp
	if diff < -relayV1EstablishExpireSkewSec || diff > relayV1EstablishExpireSkewSec {
		return fmt.Errorf("%s: connect response expire_at out of range (want near %d)", chatrelay.ErrHandshakeInvalid, exp)
	}
	now := time.Now().Unix()
	if resp.ExpireAt <= now-relayV1EstablishExpireSkewSec {
		return fmt.Errorf("%s: connect response expire_at already past", chatrelay.ErrHandshakeInvalid)
	}
	return nil
}

func relayV1ValidateHandshakeResponse(hsResp *chatrelay.RelayHandshakeResponse, localPeer, dstPeerID, sessionID string, hs *chatrelay.RelayHandshakeRequest) error {
	if hsResp == nil || hs == nil {
		return errors.New(chatrelay.ErrHandshakeInvalid)
	}
	if hsResp.Version != hs.Version {
		return errors.New(chatrelay.ErrHandshakeInvalid)
	}
	if relayV1Trim(hsResp.SessionID) != relayV1Trim(sessionID) {
		return errors.New(chatrelay.ErrHandshakeInvalid)
	}
	if relayV1Trim(hsResp.SrcID) != relayV1Trim(dstPeerID) {
		return errors.New(chatrelay.ErrHandshakeInvalid)
	}
	if relayV1Trim(hsResp.DstID) != relayV1Trim(localPeer) {
		return errors.New(chatrelay.ErrHandshakeInvalid)
	}
	if relayV1Trim(hsResp.CipherSuite) != relayV1Trim(hs.CipherSuite) {
		return errors.New(chatrelay.ErrHandshakeInvalid)
	}
	now := time.Now().Unix()
	if hsResp.Timestamp < now-relayV1EstablishHsTimestampSkewSec || hsResp.Timestamp > now+relayV1EstablishHsTimestampSkewSec {
		return errors.New(chatrelay.ErrHandshakeInvalid)
	}
	return nil
}

func (s *Service) relayV1Establish(ctx context.Context, relayPID peer.ID, dstPeerID string, st *relayV1PeerSession) error {
	log.Printf("[chat] relayV1 establish begin relay=%s dst=%s", relayPID.String(), dstPeerID)
	relayV1PeerSessionStopHeartbeatLocked(st)
	sessionID := uuid.NewString()
	ttl := 3600
	ts := time.Now().Unix()
	req := chatrelay.RelayConnectRequest{
		Version:   1,
		SessionID: sessionID,
		SrcID:     s.localPeer,
		DstID:     dstPeerID,
		Timestamp: ts,
		TTLSec:    ttl,
	}
	sig, err := chatrelay.SignBytes(s.nodePriv, chatrelay.SignPrefixConnect, chatrelay.CanonicalConnect(&req))
	if err != nil {
		return err
	}
	req.Signature = sig

	cctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	log.Printf("[chat] relayV1 connect stream dialing relay=%s dst=%s session_id=%s", relayPID.String(), dstPeerID, sessionID)
	str, err := s.host.NewStream(cctx, relayPID, chatrelay.ProtocolRelayConnect)
	if err != nil {
		return err
	}
	defer str.Close()
	_ = str.SetDeadline(time.Now().Add(40 * time.Second))
	log.Printf("[chat] relayV1 connect stream-open relay=%s dst=%s session_id=%s", relayPID.String(), dstPeerID, sessionID)
	if err := tunnel.WriteJSONFrame(str, &req); err != nil {
		return err
	}
	log.Printf("[chat] relayV1 connect write-ok relay=%s dst=%s session_id=%s", relayPID.String(), dstPeerID, sessionID)
	var resp chatrelay.RelayConnectResponse
	if err := tunnel.ReadJSONFrame(str, &resp); err != nil {
		return err
	}
	log.Printf("[chat] relayV1 connect read-ok relay=%s dst=%s session_id=%s accepted=%t", relayPID.String(), dstPeerID, sessionID, resp.Accepted)
	if !resp.Accepted {
		return fmt.Errorf("%s: %s", chatrelay.ErrRelayConnectRejected, "peer rejected")
	}
	pub, err := s.pubKeyForPeer(dstPeerID)
	if err != nil {
		return err
	}
	if err := chatrelay.VerifyEd25519(pub, chatrelay.SignPrefixConnectResp, chatrelay.CanonicalConnectResponse(&resp), resp.Signature.Value); err != nil {
		return err
	}
	if err := relayV1ValidateConnectResponse(&resp, s.localPeer, dstPeerID, relayPID, sessionID, ts, ttl); err != nil {
		return err
	}
	st.sessionID = sessionID
	st.relay = relayPID
	st.expireAt = resp.ExpireAt

	// Handshake
	priv, pubEph, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		return err
	}
	hs := chatrelay.RelayHandshakeRequest{
		Version:     1,
		SessionID:   sessionID,
		SrcID:       s.localPeer,
		DstID:       dstPeerID,
		EphPub:      base64.StdEncoding.EncodeToString(pubEph),
		CipherSuite: chatrelay.CipherSuiteX25519Chacha,
		Timestamp:   time.Now().Unix(),
	}
	canon := chatrelay.CanonicalHandshake(hs.Version, hs.SessionID, hs.SrcID, hs.DstID, hs.EphPub, hs.CipherSuite, hs.Timestamp)
	sig2, err := chatrelay.SignBytes(s.nodePriv, chatrelay.SignPrefixHandshake, canon)
	if err != nil {
		return err
	}
	hs.Signature = sig2

	cctx2, cancel2 := context.WithTimeout(ctx, 35*time.Second)
	defer cancel2()
	log.Printf("[chat] relayV1 handshake stream dialing relay=%s dst=%s session_id=%s", relayPID.String(), dstPeerID, sessionID)
	str2, err := s.host.NewStream(cctx2, relayPID, chatrelay.ProtocolRelayHandshake)
	if err != nil {
		return err
	}
	defer str2.Close()
	_ = str2.SetDeadline(time.Now().Add(40 * time.Second))
	log.Printf("[chat] relayV1 handshake stream-open relay=%s dst=%s session_id=%s", relayPID.String(), dstPeerID, sessionID)
	if err := tunnel.WriteJSONFrame(str2, &hs); err != nil {
		return err
	}
	log.Printf("[chat] relayV1 handshake write-ok relay=%s dst=%s session_id=%s", relayPID.String(), dstPeerID, sessionID)
	var hsResp chatrelay.RelayHandshakeResponse
	if err := tunnel.ReadJSONFrame(str2, &hsResp); err != nil {
		return err
	}
	log.Printf("[chat] relayV1 handshake read-ok relay=%s dst=%s session_id=%s", relayPID.String(), dstPeerID, sessionID)
	if err := relayV1ValidateHandshakeResponse(&hsResp, s.localPeer, dstPeerID, sessionID, &hs); err != nil {
		return err
	}
	pubRemote, err := base64.StdEncoding.DecodeString(strings.TrimSpace(hsResp.EphPub))
	if err != nil || len(pubRemote) != protocol.X25519KeySize {
		return errors.New(chatrelay.ErrHandshakeInvalid)
	}
	canonResp := chatrelay.CanonicalHandshake(hsResp.Version, hsResp.SessionID, hsResp.SrcID, hsResp.DstID, hsResp.EphPub, hsResp.CipherSuite, hsResp.Timestamp)
	if err := chatrelay.VerifyEd25519(pub, chatrelay.SignPrefixHandshake, canonResp, hsResp.Signature.Value); err != nil {
		return err
	}
	shared, err := protocol.X25519SharedSecret(priv, pubRemote)
	if err != nil {
		return err
	}
	tx, rx, err := chatrelay.DeriveRelaySessionKeys(shared, sessionID, true)
	if err != nil {
		return err
	}
	st.txKey, st.rxKey = tx, rx
	st.packetSeq = 0
	st.handshakeOK = true
	st.lastUsedUnix = time.Now().Unix()
	log.Printf("[chat] relayV1 establish complete relay=%s dst=%s session_id=%s expire_at=%d", relayPID.String(), dstPeerID, sessionID, st.expireAt)
	return nil
}

func (s *Service) pubKeyForPeer(peerID string) (crypto.PubKey, error) {
	pid, err := peer.Decode(strings.TrimSpace(peerID))
	if err != nil {
		return nil, err
	}
	pub := s.host.Peerstore().PubKey(pid)
	if pub != nil {
		return pub, nil
	}
	return pid.ExtractPublicKey()
}

func (s *Service) relayV1MaybeStartHeartbeat(relayPID peer.ID, dst string, st *relayV1PeerSession) {
	if st.stopHB != nil {
		return
	}
	st.stopHB = make(chan struct{})
	ch := st.stopHB
	safe.Go("chat.relayV1.heartbeat", func() {
		s.runRelayV1HeartbeatLoop(relayPID, dst, st, ch)
	})
}

func (s *Service) runRelayV1HeartbeatLoop(relayPID peer.ID, dst string, st *relayV1PeerSession, stop <-chan struct{}) {
	for {
		d := 5*time.Second + time.Duration(rand.Intn(10000))*time.Millisecond
		select {
		case <-s.ctx.Done():
			return
		case <-stop:
			return
		case <-time.After(d):
		}
		st.mu.Lock()
		sid := st.sessionID
		exp := st.expireAt
		st.mu.Unlock()
		if sid == "" || time.Now().Unix() >= exp-2 {
			return
		}
		ping := chatrelay.RelayHeartbeat{
			Version:   1,
			SessionID: sid,
			SrcID:     s.localPeer,
			DstID:     dst,
			RelayID:   relayPID.String(),
			PingID:    uuid.NewString(),
			SentAt:    time.Now().Unix(),
		}
		canon := chatrelay.CanonicalHeartbeat(&ping)
		sig, err := chatrelay.SignBytes(s.nodePriv, chatrelay.SignPrefixHeartbeat, canon)
		if err != nil {
			st.mu.Lock()
			st.hbFail++
			st.mu.Unlock()
			continue
		}
		ping.Signature = sig
		cctx, cancel := context.WithTimeout(s.ctx, 12*time.Second)
		str, err := s.host.NewStream(cctx, relayPID, chatrelay.ProtocolRelayHeartbeat)
		cancel()
		if err != nil {
			st.mu.Lock()
			st.hbFail++
			f := st.hbFail
			st.mu.Unlock()
			if f >= 5 {
				p := dst
				if len(p) > 8 {
					p = p[:8]
				}
				log.Printf("[chat] relay-v1 heartbeat: degraded dst=%s", p)
			}
			continue
		}
		_ = str.SetDeadline(time.Now().Add(25 * time.Second))
		if err := tunnel.WriteJSONFrame(str, &ping); err != nil {
			_ = str.Close()
			st.mu.Lock()
			st.hbFail++
			st.mu.Unlock()
			continue
		}
		var pong chatrelay.RelayHeartbeat
		if err := tunnel.ReadJSONFrame(str, &pong); err != nil {
			_ = str.Close()
			st.mu.Lock()
			st.hbFail++
			st.mu.Unlock()
			continue
		}
		_ = str.Close()
		if err := s.relayV1VerifyHeartbeatPong(&ping, &pong, relayPID, dst); err != nil {
			st.mu.Lock()
			st.hbFail++
			f := st.hbFail
			st.mu.Unlock()
			if f >= 5 {
				p := dst
				if len(p) > 8 {
					p = p[:8]
				}
				log.Printf("[chat] relay-v1 heartbeat: invalid pong dst=%s: %v", p, err)
			}
			continue
		}
		st.mu.Lock()
		st.hbFail = 0
		st.mu.Unlock()
	}
}

// relayV1VerifyHeartbeatPong 校驗 pong 與本輪 ping 上下文一致，並用對端公鑰驗簽（防止錯包/重放/偽造）。
func (s *Service) relayV1VerifyHeartbeatPong(ping *chatrelay.RelayHeartbeat, pong *chatrelay.RelayHeartbeat, relayPID peer.ID, dstPeerID string) error {
	if ping == nil || pong == nil {
		return errors.New("nil heartbeat frame")
	}
	if strings.TrimSpace(pong.SessionID) != strings.TrimSpace(ping.SessionID) {
		return fmt.Errorf("session_id mismatch")
	}
	if strings.TrimSpace(pong.SrcID) != strings.TrimSpace(dstPeerID) {
		return fmt.Errorf("src_id mismatch want peer %s", dstPeerID)
	}
	if strings.TrimSpace(pong.DstID) != strings.TrimSpace(s.localPeer) {
		return fmt.Errorf("dst_id mismatch")
	}
	if strings.TrimSpace(pong.RelayID) != strings.TrimSpace(relayPID.String()) {
		return fmt.Errorf("relay_id mismatch")
	}
	if strings.TrimSpace(pong.PingID) != strings.TrimSpace(ping.PingID) {
		return fmt.Errorf("ping_id mismatch")
	}
	pub, err := s.pubKeyForPeer(dstPeerID)
	if err != nil {
		return err
	}
	canon := chatrelay.CanonicalHeartbeat(pong)
	return chatrelay.VerifyEd25519(pub, chatrelay.SignPrefixHeartbeat, canon, pong.Signature.Value)
}

// --- B 端入站（由 app 調度，首幀可能已讀）---

// ServeRelayConnectAsB 處理 connect（首幀已解析為 req）。
func (s *Service) ServeRelayConnectAsB(str network.Stream, req *chatrelay.RelayConnectRequest) {
	defer str.Close()
	_ = str.SetDeadline(time.Now().Add(40 * time.Second))
	if req == nil || strings.TrimSpace(req.DstID) != s.localPeer {
		return
	}
	now := time.Now().Unix()
	if now > req.Timestamp+int64(req.TTLSec) {
		return
	}
	pub, err := s.pubKeyForPeer(req.SrcID)
	if err != nil {
		return
	}
	if err := chatrelay.VerifyEd25519(pub, chatrelay.SignPrefixConnect, chatrelay.CanonicalConnect(req), req.Signature.Value); err != nil {
		return
	}
	if !s.relayV1BTryRegisterConnect(req.SessionID, req.SrcID, int64(req.TTLSec)+req.Timestamp) {
		return
	}
	resp := chatrelay.RelayConnectResponse{
		Version:   1,
		SessionID: req.SessionID,
		SrcID:     s.localPeer,
		DstID:     req.SrcID,
		RelayID:   str.Conn().RemotePeer().String(),
		Accepted:  true,
		ExpireAt:  req.Timestamp + int64(req.TTLSec),
	}
	sig, err := chatrelay.SignBytes(s.nodePriv, chatrelay.SignPrefixConnectResp, chatrelay.CanonicalConnectResponse(&resp))
	if err != nil {
		return
	}
	resp.Signature = sig
	_ = tunnel.WriteJSONFrame(str, &resp)
}

// ServeRelayHandshakeAsB 處理握手。
func (s *Service) ServeRelayHandshakeAsB(str network.Stream, req *chatrelay.RelayHandshakeRequest) {
	defer str.Close()
	_ = str.SetDeadline(time.Now().Add(40 * time.Second))
	if req == nil || strings.TrimSpace(req.DstID) != s.localPeer {
		return
	}
	if !s.relayV1BHasConnect(req.SessionID, req.SrcID) {
		return
	}
	pub, err := s.pubKeyForPeer(req.SrcID)
	if err != nil {
		return
	}
	canon := chatrelay.CanonicalHandshake(req.Version, req.SessionID, req.SrcID, req.DstID, req.EphPub, req.CipherSuite, req.Timestamp)
	if err := chatrelay.VerifyEd25519(pub, chatrelay.SignPrefixHandshake, canon, req.Signature.Value); err != nil {
		return
	}
	hsKey := relayV1BKey(req.SessionID, req.SrcID)
	// 一次性握手：已存在 (session_id, src_id) 密钥则拒绝重放，避免覆盖 tx/rx 与 packetSeq 导致与 A 静默失步。
	s.relayV1BMu.Lock()
	s.relayV1BMaybePurgeLocked(time.Now())
	if !s.relayV1BConnectValidLocked(req.SessionID, req.SrcID) {
		s.relayV1BMu.Unlock()
		return
	}
	if s.relayV1BKeys != nil {
		if _, exists := s.relayV1BKeys[hsKey]; exists {
			s.relayV1BMu.Unlock()
			return
		}
	}
	s.relayV1BMu.Unlock()

	priv, pubEph, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		return
	}
	remotePub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.EphPub))
	if err != nil || len(remotePub) != protocol.X25519KeySize {
		return
	}
	shared, err := protocol.X25519SharedSecret(priv, remotePub)
	if err != nil {
		return
	}
	tx, rx, err := chatrelay.DeriveRelaySessionKeys(shared, req.SessionID, false)
	if err != nil {
		return
	}

	resp := chatrelay.RelayHandshakeResponse{
		Version:     1,
		SessionID:   req.SessionID,
		SrcID:       s.localPeer,
		DstID:       req.SrcID,
		EphPub:      base64.StdEncoding.EncodeToString(pubEph),
		CipherSuite: chatrelay.CipherSuiteX25519Chacha,
		Timestamp:   time.Now().Unix(),
	}
	canonR := chatrelay.CanonicalHandshake(resp.Version, resp.SessionID, resp.SrcID, resp.DstID, resp.EphPub, resp.CipherSuite, resp.Timestamp)
	sig, err := chatrelay.SignBytes(s.nodePriv, chatrelay.SignPrefixHandshake, canonR)
	if err != nil {
		return
	}
	resp.Signature = sig

	s.relayV1BMu.Lock()
	s.relayV1BMaybePurgeLocked(time.Now())
	if !s.relayV1BConnectValidLocked(req.SessionID, req.SrcID) {
		s.relayV1BMu.Unlock()
		return
	}
	if s.relayV1BKeys != nil {
		if _, exists := s.relayV1BKeys[hsKey]; exists {
			s.relayV1BMu.Unlock()
			return
		}
	}
	s.relayV1BStoreKeysLocked(hsKey, tx, rx)
	s.relayV1BMu.Unlock()

	if err := tunnel.WriteJSONFrame(str, &resp); err != nil {
		return
	}
	// B 端入站握手成功：登記面向 A（src）的可復用發送路由，後續 sendViaRelay 會優先走同一 R + session_id。
	expireAt := s.relayV1BConnectExpire(req.SessionID)
	if expireAt > 0 {
		s.relayV1ActivateOutboundSession(req.SrcID, str.Conn().RemotePeer(), req.SessionID, expireAt, tx, rx)
	}
}

// relayV1ActivateOutboundSession 在 B 完成與 A 的 relay 握手後，寫入與 A 通信用的 relayV1PeerSession（與 A 端對稱）。
func (s *Service) relayV1ActivateOutboundSession(dstPeerID string, relayPID peer.ID, sessionID string, expireAt int64, txSend, rxRecv []byte) {
	st := s.relayV1SessionFor(dstPeerID)
	st.mu.Lock()
	defer st.mu.Unlock()
	relayV1PeerSessionStopHeartbeatLocked(st)
	st.relay = relayPID
	st.sessionID = sessionID
	st.expireAt = expireAt
	st.txKey, st.rxKey = txSend, rxRecv
	st.packetSeq = 0
	st.handshakeOK = true
	st.lastUsedUnix = time.Now().Unix()
}

// relayV1PreferredRelayForDst 若對該 peer 已有未過期的 relay-v1 會話，返回綁定的中繼（供 sendViaRelay 優先使用）。
func (s *Service) relayV1PreferredRelayForDst(dstPeerID string) (peer.ID, bool) {
	s.relayV1SessionsMu.Lock()
	s.relayV1MaybePurgeStaleRelaySessionsLocked()
	if s.relayV1Sessions == nil {
		s.relayV1SessionsMu.Unlock()
		return "", false
	}
	st, ok := s.relayV1Sessions[dstPeerID]
	s.relayV1SessionsMu.Unlock()
	if !ok || st == nil {
		return "", false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.handshakeOK || st.sessionID == "" {
		return "", false
	}
	if time.Now().Unix() >= st.expireAt-5 {
		return "", false
	}
	if st.relay == "" {
		return "", false
	}
	st.lastUsedUnix = time.Now().Unix()
	return st.relay, true
}

func relayV1DedupePrepend(pref peer.ID, relays []peer.ID) []peer.ID {
	if pref == "" {
		return relays
	}
	out := make([]peer.ID, 0, len(relays)+1)
	out = append(out, pref)
	for _, r := range relays {
		if r == pref {
			continue
		}
		out = append(out, r)
	}
	return out
}

// relayV1RelayCandidatesWithPreferred 在原有候選基礎上，若有已建立 relay-v1 則把對應中繼置於首位。
func (s *Service) relayV1RelayCandidatesWithPreferred(peerID string, limit int) ([]peer.ID, error) {
	base, err := s.pickRelayCandidates(peerID, limit)
	if err != nil {
		return nil, err
	}
	if pref, ok := s.relayV1PreferredRelayForDst(peerID); ok {
		return relayV1DedupePrepend(pref, base), nil
	}
	return base, nil
}

// ServeRelayDataAsB 解密並交業務層。
func (s *Service) ServeRelayDataAsB(str network.Stream, frame *chatrelay.RelayDataFrame) {
	defer str.Close()
	_ = str.SetDeadline(time.Now().Add(45 * time.Second))
	if frame == nil || strings.TrimSpace(frame.DstID) != s.localPeer {
		return
	}
	_, rx, ok := s.relayV1BGetKeys(frame.SessionID, frame.SrcID)
	if !ok {
		return
	}
	nonce, err := base64.StdEncoding.DecodeString(frame.Cipher.Nonce)
	if err != nil {
		return
	}
	ct, err := base64.StdEncoding.DecodeString(frame.Cipher.Ciphertext)
	if err != nil {
		return
	}
	plain, err := chatrelay.OpenDataFrame(rx, frame.SessionID, frame.PacketSeq, nonce, ct)
	if err != nil {
		return
	}
	for _, chunk := range s.relayV1BIngestRelayDataPacket(frame.SessionID, frame.SrcID, frame.PacketSeq, plain) {
		_ = s.processEnvelopeBytes(chunk)
	}
}

// relayV1BHeartbeatAllowedAndHasConnect 惰性 purge、心跳限流並校驗 connect 仍有效（單鎖）。
func (s *Service) relayV1BHeartbeatAllowedAndHasConnect(sessionID, srcID string) bool {
	s.relayV1BMu.Lock()
	defer s.relayV1BMu.Unlock()
	now := time.Now()
	s.relayV1BMaybePurgeLocked(now)
	if s.relayV1BLastHbAt == nil {
		s.relayV1BLastHbAt = make(map[string]time.Time)
	}
	if last, ok := s.relayV1BLastHbAt[srcID]; ok && now.Sub(last) < relayV1BMinHeartbeatGap {
		return false
	}
	s.relayV1BLastHbAt[srcID] = now
	r, ok := s.relayV1BConnect[sessionID]
	if !ok || r.srcID != srcID {
		return false
	}
	return now.Unix() <= r.expireAt
}

// ServeRelayHeartbeatAsB 響應心跳（同幀類型回覆 pong）。
func (s *Service) ServeRelayHeartbeatAsB(str network.Stream, ping *chatrelay.RelayHeartbeat) {
	defer str.Close()
	_ = str.SetDeadline(time.Now().Add(25 * time.Second))
	if ping == nil || strings.TrimSpace(ping.DstID) != s.localPeer {
		return
	}
	if !s.relayV1BHeartbeatAllowedAndHasConnect(ping.SessionID, ping.SrcID) {
		return
	}
	pub, err := s.pubKeyForPeer(ping.SrcID)
	if err != nil {
		return
	}
	if err := chatrelay.VerifyEd25519(pub, chatrelay.SignPrefixHeartbeat, chatrelay.CanonicalHeartbeat(ping), ping.Signature.Value); err != nil {
		return
	}
	pong := chatrelay.RelayHeartbeat{
		Version:   1,
		SessionID: ping.SessionID,
		SrcID:     s.localPeer,
		DstID:     ping.SrcID,
		RelayID:   ping.RelayID,
		PingID:    ping.PingID,
		SentAt:    time.Now().Unix(),
	}
	sig, err := chatrelay.SignBytes(s.nodePriv, chatrelay.SignPrefixHeartbeat, chatrelay.CanonicalHeartbeat(&pong))
	if err != nil {
		return
	}
	pong.Signature = sig
	_ = tunnel.WriteJSONFrame(str, &pong)
}

func (s *Service) relayV1BHasConnect(sessionID, srcID string) bool {
	s.relayV1BMu.Lock()
	defer s.relayV1BMu.Unlock()
	s.relayV1BMaybePurgeLocked(time.Now())
	r, ok := s.relayV1BConnect[sessionID]
	if !ok || r.srcID != srcID {
		return false
	}
	return time.Now().Unix() <= r.expireAt
}

func (s *Service) relayV1BConnectExpire(sessionID string) int64 {
	s.relayV1BMu.Lock()
	defer s.relayV1BMu.Unlock()
	s.relayV1BMaybePurgeLocked(time.Now())
	r, ok := s.relayV1BConnect[sessionID]
	if !ok {
		return 0
	}
	return r.expireAt
}

// relayV1BStoreKeysLocked 寫入握手導出的密鑰表項（須已持 relayV1BMu）。
func (s *Service) relayV1BStoreKeysLocked(mapKey string, tx, rx []byte) {
	if s.relayV1BKeys == nil {
		s.relayV1BKeys = make(map[string]relayV1KeyEntry)
	}
	s.relayV1BKeys[mapKey] = relayV1KeyEntry{tx: tx, rx: rx, lastDataSeq: ^uint64(0), pending: nil}
}

func (s *Service) relayV1BGetKeys(sessionID, srcID string) (tx, rx []byte, ok bool) {
	s.relayV1BMu.Lock()
	defer s.relayV1BMu.Unlock()
	s.relayV1BMaybePurgeLocked(time.Now())
	k, ok := s.relayV1BKeys[relayV1BKey(sessionID, srcID)]
	if !ok {
		return nil, nil, false
	}
	return k.tx, k.rx, true
}

// relayV1BIngestRelayDataPacket 在持鎖下更新序號前沿；返回應按序交給業務層的明文切片（可能多段，因 flush pending）。
func (s *Service) relayV1BIngestRelayDataPacket(sessionID, srcID string, seq uint64, plain []byte) [][]byte {
	s.relayV1BMu.Lock()
	defer s.relayV1BMu.Unlock()
	s.relayV1BMaybePurgeLocked(time.Now())
	key := relayV1BKey(sessionID, srcID)
	k, ok := s.relayV1BKeys[key]
	if !ok {
		return nil
	}

	nextExpected := uint64(0)
	if k.lastDataSeq != ^uint64(0) {
		nextExpected = k.lastDataSeq + 1
	}

	// 已連續交付過的包：重複
	if k.lastDataSeq != ^uint64(0) && seq <= k.lastDataSeq {
		return nil
	}
	if k.pending != nil {
		if _, dup := k.pending[seq]; dup {
			return nil
		}
	}

	var out [][]byte
	push := func(p []byte) {
		pc := make([]byte, len(p))
		copy(pc, p)
		out = append(out, pc)
	}

	if seq == nextExpected {
		push(plain)
		k.lastDataSeq = seq
		for {
			nx := k.lastDataSeq + 1
			pend, ok2 := k.pending[nx]
			if !ok2 {
				break
			}
			delete(k.pending, nx)
			push(pend)
			k.lastDataSeq = nx
		}
		if len(k.pending) == 0 {
			k.pending = nil
		}
		s.relayV1BKeys[key] = k
		return out
	}

	if seq < nextExpected {
		return nil
	}
	gap := seq - nextExpected
	if gap > relayV1BMaxDataReorderGap {
		return nil
	}
	if k.pending == nil {
		k.pending = make(map[uint64][]byte)
	}
	if len(k.pending) >= relayV1BMaxDataPendingSlots {
		return nil
	}
	pc := make([]byte, len(plain))
	copy(pc, plain)
	k.pending[seq] = pc
	s.relayV1BKeys[key] = k
	return nil
}
