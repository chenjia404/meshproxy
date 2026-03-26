// Package relay：ChatRelay V1（《聊天中继.md》）中繼轉發。
package relay

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/chenjia404/meshproxy/internal/chatrelay"
	"github.com/chenjia404/meshproxy/internal/traffic"
	"github.com/chenjia404/meshproxy/internal/tunnel"
)

// MaxRelayV1FrameBytes 單幀上限（對應錯誤 RELAY_PAYLOAD_TOO_LARGE）。
const MaxRelayV1FrameBytes = 12 << 20

// 防資源耗盡：惰性清理 + 每源會話上限 + 建鏈/心跳限流。
const (
	chatRelayV1MaxSessionsPerSrc = 64
	chatRelayV1MaxSessionsTotal  = 8192
	chatRelayV1PurgeInterval     = 2 * time.Second
	chatRelayV1MinConnectGap     = 200 * time.Millisecond
	chatRelayV1MinHeartbeatGap   = 400 * time.Millisecond
)

// ChatRelayV1Table 中繼側按 session_id 登記的轉發表（文檔十一.4）。
type ChatRelayV1Table struct {
	mu       sync.RWMutex
	sessions map[string]chatRelayV1Reg
	bySrc    map[string][]string // 每 src 的 session_id FIFO（便於按源淘汰）

	lastPurge       time.Time
	lastConnectAt   map[string]time.Time // 入站 RemotePeer().String() -> 上次允許的 connect 轉發
	lastHeartbeatAt map[string]time.Time // 入站 RemotePeer().String() -> 上次允許的 heartbeat 轉發
}

type chatRelayV1Reg struct {
	srcID     string
	dstID     string
	expireAt  int64
	createdAt int64
}

// NewChatRelayV1Table 建立空表。
func NewChatRelayV1Table() *ChatRelayV1Table {
	return &ChatRelayV1Table{
		sessions:        make(map[string]chatRelayV1Reg),
		bySrc:           make(map[string][]string),
		lastConnectAt:   make(map[string]time.Time),
		lastHeartbeatAt: make(map[string]time.Time),
	}
}

func removeStringFromSlice(s []string, x string) []string {
	out := s[:0]
	for _, y := range s {
		if y != x {
			out = append(out, y)
		}
	}
	return out
}

// maybePurgeExpiredLocked 惰性刪除過期項並在總量過大時淘汰（須已持鎖）；全表掃描節流以免熱路徑 O(n) 過頻。
func (t *ChatRelayV1Table) maybePurgeExpiredLocked(now time.Time) {
	nowUnix := now.Unix()
	heavy := t.lastPurge.IsZero() || now.Sub(t.lastPurge) >= chatRelayV1PurgeInterval || len(t.sessions) > chatRelayV1MaxSessionsTotal
	if !heavy {
		return
	}
	t.lastPurge = now
	for sid, r := range t.sessions {
		if nowUnix > r.expireAt {
			t.removeSessionLocked(sid)
		}
	}
	for len(t.sessions) > chatRelayV1MaxSessionsTotal {
		n := len(t.sessions)
		t.evictOneSessionLocked()
		if len(t.sessions) >= n {
			break
		}
	}
}

func (t *ChatRelayV1Table) removeSessionLocked(sid string) {
	r, ok := t.sessions[sid]
	if !ok {
		return
	}
	delete(t.sessions, sid)
	if t.bySrc != nil {
		t.bySrc[r.srcID] = removeStringFromSlice(t.bySrc[r.srcID], sid)
	}
}

// evictOneSessionLocked 刪除 expireAt 最早的一條（總量超限時）。
func (t *ChatRelayV1Table) evictOneSessionLocked() {
	var victim string
	var minExp int64 = 1<<62 - 1
	for sid, r := range t.sessions {
		if r.expireAt < minExp {
			minExp = r.expireAt
			victim = sid
		}
	}
	if victim != "" {
		t.removeSessionLocked(victim)
	}
}

func (t *ChatRelayV1Table) enforcePerSrcCapLocked(srcID string) {
	for t.bySrc != nil && len(t.bySrc[srcID]) >= chatRelayV1MaxSessionsPerSrc {
		ids := t.bySrc[srcID]
		if len(ids) == 0 {
			break
		}
		t.removeSessionLocked(ids[0])
	}
}

// AllowConnectForward 建鏈轉發限流（每入站連線最小間隔）。inboundPeerID 須為 str.Conn().RemotePeer().String()，不可使用 payload 內 src_id。
func (t *ChatRelayV1Table) AllowConnectForward(inboundPeerID string) bool {
	if t == nil || inboundPeerID == "" {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if last, ok := t.lastConnectAt[inboundPeerID]; ok && now.Sub(last) < chatRelayV1MinConnectGap {
		return false
	}
	t.lastConnectAt[inboundPeerID] = now
	return true
}

// AllowHeartbeatForward 心跳轉發限流（每入站連線最小間隔）。inboundPeerID 須為 str.Conn().RemotePeer().String()，不可使用 payload 內 src_id。
func (t *ChatRelayV1Table) AllowHeartbeatForward(inboundPeerID string) bool {
	if t == nil || inboundPeerID == "" {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if last, ok := t.lastHeartbeatAt[inboundPeerID]; ok && now.Sub(last) < chatRelayV1MinHeartbeatGap {
		return false
	}
	t.lastHeartbeatAt[inboundPeerID] = now
	return true
}

// RegisterSession connect 成功後登記。
func (t *ChatRelayV1Table) RegisterSession(sessionID, srcID, dstID string, expireAt int64) {
	if t == nil || sessionID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.maybePurgeExpiredLocked(now)
	t.enforcePerSrcCapLocked(srcID)
	nowUnix := now.Unix()
	t.sessions[sessionID] = chatRelayV1Reg{srcID: srcID, dstID: dstID, expireAt: expireAt, createdAt: nowUnix}
	if t.bySrc == nil {
		t.bySrc = make(map[string][]string)
	}
	t.bySrc[srcID] = append(t.bySrc[srcID], sessionID)
}

// ValidSession 校驗 session 存在且未過期、src/dst 與幀一致。
func (t *ChatRelayV1Table) ValidSession(sessionID, srcID, dstID string, nowUnix int64) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maybePurgeExpiredLocked(time.Unix(nowUnix, 0))
	r, ok := t.sessions[sessionID]
	if !ok || nowUnix > r.expireAt {
		return false
	}
	return r.srcID == srcID && r.dstID == dstID
}

func relayV1RecordJSON(tr *traffic.Recorder, v any) {
	if tr == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	tr.Add(0, uint64(len(b)))
}

// relayV1InboundSrcMatchesRemotePeer 校驗 JSON 聲明的 src_id 可解析為 peer.ID 且與實際入站連線一致（防偽造 src 繞過限流／污染轉發）。
func relayV1InboundSrcMatchesRemotePeer(remote peer.ID, claimedSrcID string) bool {
	if remote == "" || strings.TrimSpace(claimedSrcID) == "" {
		return false
	}
	pid, err := peer.Decode(strings.TrimSpace(claimedSrcID))
	if err != nil {
		return false
	}
	return pid == remote
}

func relayV1SigBlockPresent(sig chatrelay.SignatureBlock) bool {
	return strings.TrimSpace(sig.Algorithm) != "" && strings.TrimSpace(sig.Value) != ""
}

// ForwardRelayConnect 將 A 的 connect 轉發給 B 並把響應寫回 A；str 為 A→R 的流（首幀已讀入 req）。
func ForwardRelayConnect(ctx context.Context, h host.Host, tbl *ChatRelayV1Table, tr *traffic.Recorder, str network.Stream, req *chatrelay.RelayConnectRequest) {
	defer str.Close()
	_ = str.SetDeadline(time.Now().Add(45 * time.Second))
	if req == nil {
		return
	}
	inbound := str.Conn().RemotePeer()
	inboundStr := inbound.String()
	if tbl != nil && !tbl.AllowConnectForward(inboundStr) {
		return
	}
	if !relayV1InboundSrcMatchesRemotePeer(inbound, req.SrcID) || !relayV1SigBlockPresent(req.Signature) {
		return
	}
	dstPID, err := peer.Decode(strings.TrimSpace(req.DstID))
	if err != nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	nextStr, err := h.NewStream(cctx, dstPID, chatrelay.ProtocolRelayConnect)
	if err != nil {
		prefix := req.DstID
		if len(prefix) > 8 {
			prefix = prefix[:8]
		}
		// log.Printf("[relay-v1] connect forward dial dst=%s: %v", prefix, err)
		return
	}
	defer nextStr.Close()
	_ = nextStr.SetDeadline(time.Now().Add(45 * time.Second))
	if err := tunnel.WriteJSONFrame(nextStr, req); err != nil {
		return
	}
	relayV1RecordJSON(tr, req)
	var resp chatrelay.RelayConnectResponse
	if err := tunnel.ReadJSONFrame(nextStr, &resp); err != nil {
		return
	}
	if resp.Accepted && resp.SessionID == req.SessionID && tbl != nil {
		tbl.RegisterSession(req.SessionID, req.SrcID, req.DstID, resp.ExpireAt)
	}
	relayV1RecordJSON(tr, resp)
	if err := tunnel.WriteJSONFrame(str, &resp); err != nil {
		return
	}
}

// ForwardRelayHandshake 轉發握手請求/響應（單輪請求-響應）。須先有本中繼上已成功 connect 並登記的 session，否則不撥下游（減少無 connect 的握手 DoS）。
func ForwardRelayHandshake(ctx context.Context, h host.Host, tbl *ChatRelayV1Table, tr *traffic.Recorder, str network.Stream, req *chatrelay.RelayHandshakeRequest) {
	defer str.Close()
	_ = str.SetDeadline(time.Now().Add(45 * time.Second))
	if req == nil {
		return
	}
	inbound := str.Conn().RemotePeer()
	if !relayV1InboundSrcMatchesRemotePeer(inbound, req.SrcID) || !relayV1SigBlockPresent(req.Signature) {
		return
	}
	now := time.Now().Unix()
	if tbl == nil || !tbl.ValidSession(req.SessionID, req.SrcID, req.DstID, now) {
		return
	}
	dstPID, err := peer.Decode(strings.TrimSpace(req.DstID))
	if err != nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	nextStr, err := h.NewStream(cctx, dstPID, chatrelay.ProtocolRelayHandshake)
	if err != nil {
		return
	}
	defer nextStr.Close()
	_ = nextStr.SetDeadline(time.Now().Add(45 * time.Second))
	if err := tunnel.WriteJSONFrame(nextStr, req); err != nil {
		return
	}
	relayV1RecordJSON(tr, req)
	var resp chatrelay.RelayHandshakeResponse
	if err := tunnel.ReadJSONFrame(nextStr, &resp); err != nil {
		return
	}
	relayV1RecordJSON(tr, resp)
	_ = tunnel.WriteJSONFrame(str, &resp)
}

// ForwardRelayData 轉發數據幀（不解密）。
func ForwardRelayData(ctx context.Context, h host.Host, tbl *ChatRelayV1Table, tr *traffic.Recorder, str network.Stream, frame *chatrelay.RelayDataFrame) {
	defer str.Close()
	_ = str.SetDeadline(time.Now().Add(45 * time.Second))
	if frame == nil {
		return
	}
	inbound := str.Conn().RemotePeer()
	if !relayV1InboundSrcMatchesRemotePeer(inbound, frame.SrcID) {
		return
	}
	now := time.Now().Unix()
	if tbl == nil || !tbl.ValidSession(frame.SessionID, frame.SrcID, frame.DstID, now) {
		return
	}
	if approxRelayDataSize(frame) > MaxRelayV1FrameBytes {
		return
	}
	dstPID, err := peer.Decode(strings.TrimSpace(frame.DstID))
	if err != nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	nextStr, err := h.NewStream(cctx, dstPID, chatrelay.ProtocolRelayData)
	if err != nil {
		return
	}
	defer nextStr.Close()
	_ = nextStr.SetDeadline(time.Now().Add(45 * time.Second))
	if err := tunnel.WriteJSONFrame(nextStr, frame); err != nil {
		return
	}
	relayV1RecordJSON(tr, frame)
}

func approxRelayDataSize(f *chatrelay.RelayDataFrame) int {
	b, err := json.Marshal(f)
	if err != nil {
		return MaxRelayV1FrameBytes + 1
	}
	return len(b)
}

// ForwardRelayHeartbeat 轉發心跳並回傳響應（B 在同流上寫回一幀 RelayHeartbeat 作為 pong）。
func ForwardRelayHeartbeat(ctx context.Context, h host.Host, tbl *ChatRelayV1Table, tr *traffic.Recorder, str network.Stream, ping *chatrelay.RelayHeartbeat) {
	defer str.Close()
	_ = str.SetDeadline(time.Now().Add(25 * time.Second))
	if ping == nil {
		return
	}
	inbound := str.Conn().RemotePeer()
	inboundStr := inbound.String()
	if tbl != nil && !tbl.AllowHeartbeatForward(inboundStr) {
		return
	}
	if !relayV1InboundSrcMatchesRemotePeer(inbound, ping.SrcID) || !relayV1SigBlockPresent(ping.Signature) {
		return
	}
	now := time.Now().Unix()
	if tbl == nil || !tbl.ValidSession(ping.SessionID, ping.SrcID, ping.DstID, now) {
		return
	}
	dstPID, err := peer.Decode(strings.TrimSpace(ping.DstID))
	if err != nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	nextStr, err := h.NewStream(cctx, dstPID, chatrelay.ProtocolRelayHeartbeat)
	if err != nil {
		return
	}
	defer nextStr.Close()
	_ = nextStr.SetDeadline(time.Now().Add(25 * time.Second))
	if err := tunnel.WriteJSONFrame(nextStr, ping); err != nil {
		return
	}
	relayV1RecordJSON(tr, ping)
	var pong chatrelay.RelayHeartbeat
	if err := tunnel.ReadJSONFrame(nextStr, &pong); err != nil {
		return
	}
	relayV1RecordJSON(tr, pong)
	_ = tunnel.WriteJSONFrame(str, &pong)
}
