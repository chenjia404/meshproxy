package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"

	"meshproxy/internal/protocol"
	"meshproxy/internal/store"
)

// CircuitManager manages circuit lifecycle and delegates storage to CircuitStore.
type CircuitManager struct {
	mu           sync.Mutex
	ctx          context.Context
	host         host.Host
	store        *store.CircuitStore
	pathSelector *PathSelector
	streams      *StreamManager
	pool             *CircuitPool
	buildRetries     int // 1~2: retries when building circuit fails (relay/exit failover)
	beginTCPRetries  int // 1~2: retries when exit connect (BEGIN_TCP) fails
	// activeStreams keeps the libp2p streams per circuit.
	activeStreams map[string]network.Stream
	// hopSessions 每條 circuit 的逐跳會話（順序 [relay, exit]），用於洋蔥加解密；不暴露給 API。
	hopSessions map[string][]*protocol.HopSession
	// pendingConnected stores waiting channels for CONNECTED messages, keyed by streamID.
	pendingConnected map[string]chan protocol.Connected
}

// NewCircuitManager creates a new CircuitManager.
func NewCircuitManager(ctx context.Context, h host.Host, store *store.CircuitStore, selector *PathSelector, streams *StreamManager) *CircuitManager {
	return &CircuitManager{
		ctx:              ctx,
		host:             h,
		store:            store,
		pathSelector:     selector,
		streams:          streams,
		activeStreams:    make(map[string]network.Stream),
		hopSessions:      make(map[string][]*protocol.HopSession),
		pendingConnected: make(map[string]chan protocol.Connected),
	}
}

// ListCircuits returns a snapshot of all circuits.
func (m *CircuitManager) ListCircuits() []protocol.CircuitInfo {
	return m.store.GetAll()
}

// SetPool sets the circuit pool (called after NewCircuitManager when pool is created).
func (m *CircuitManager) SetPool(pool *CircuitPool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pool = pool
}

// SetBuildRetries sets the number of retries when circuit build fails (0, 1, or 2).
func (m *CircuitManager) SetBuildRetries(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n < 0 {
		n = 0
	}
	if n > 2 {
		n = 2
	}
	m.buildRetries = n
}

// SetBeginTCPRetries sets the number of retries when BEGIN_TCP (exit connect) fails (0, 1, or 2).
func (m *CircuitManager) SetBeginTCPRetries(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n < 0 {
		n = 0
	}
	if n > 2 {
		n = 2
	}
	m.beginTCPRetries = n
}

// BeginTCPRetries returns the configured number of retries for BEGIN_TCP.
func (m *CircuitManager) BeginTCPRetries() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.beginTCPRetries
}

// GetPlan returns the path plan for a circuit (for failover: get exit peer to exclude).
func (m *CircuitManager) GetPlan(circuitID string) (protocol.PathPlan, bool) {
	rec, ok := m.store.Get(circuitID)
	if !ok || rec == nil {
		return protocol.PathPlan{}, false
	}
	return rec.Plan, true
}

// GetPoolStatus returns circuit pool status for API (nil if no pool).
func (m *CircuitManager) GetPoolStatus() *PoolStatus {
	m.mu.Lock()
	pool := m.pool
	m.mu.Unlock()
	if pool == nil {
		return nil
	}
	s := pool.Status()
	return &s
}

// getHopSessions 返回該 circuit 的逐跳會話（順序 [relay, exit]），調用方不得修改；無會話時返回 nil。
func (m *CircuitManager) getHopSessions(circuitID string) []*protocol.HopSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hopSessions[circuitID]
}

// EnsureCircuit selects a path and ensures there is an open circuit for it.
func (m *CircuitManager) EnsureCircuit(plan protocol.PathPlan) (string, error) {
	return m.createCircuitWithPlan(plan)
}

// EnsureCircuitFromPool gets a circuit from the pool for the given kind, or creates one on demand.
// Use ReturnToPool when the connection ends successfully, or MarkCircuitFailed on error.
func (m *CircuitManager) EnsureCircuitFromPool(kind PoolKind) (string, error) {
	m.mu.Lock()
	pool := m.pool
	m.mu.Unlock()
	if pool != nil {
		if id, ok := pool.GetFromPool(kind); ok {
			return id, nil
		}
	}
	return m.CreateForPool(kind)
}

// CreateForPool creates a new circuit for the given pool kind (implements CircuitPoolFactory).
// Uses build retry with relay/exit failover when creation fails.
func (m *CircuitManager) CreateForPool(kind PoolKind) (string, error) {
	return m.CreateForPoolExcluding(kind, nil)
}

// CreateForPoolExcluding creates a new circuit excluding given peer IDs, with retries (relay/exit failover).
func (m *CircuitManager) CreateForPoolExcluding(kind PoolKind, excludePeerIDs []string) (string, error) {
	m.mu.Lock()
	retries := m.buildRetries
	m.mu.Unlock()

	exclude := make([]string, 0, len(excludePeerIDs)+retries+1)
	exclude = append(exclude, excludePeerIDs...)

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		plan, err := m.pathSelector.SelectPathForPoolExcluding(kind, exclude)
		if err != nil {
			return "", err
		}
		id, err := m.createCircuitWithPlan(plan)
		if err == nil {
			return id, nil
		}
		lastErr = err
		// Exclude this path's exit so next attempt uses another relay/exit
		exitPeerID := plan.Hops[plan.ExitHopIndex].PeerID
		exclude = append(exclude, exitPeerID)
	}
	return "", lastErr
}

// CloseCircuit closes the circuit and stream (implements CircuitPoolFactory).
func (m *CircuitManager) CloseCircuit(circuitID string) {
	m.mu.Lock()
	s, ok := m.activeStreams[circuitID]
	delete(m.activeStreams, circuitID)
	rec, _ := m.store.Get(circuitID)
	if rec != nil {
		rec.State = protocol.CircuitClosed
		rec.UpdatedAt = time.Now()
		m.store.Upsert(rec)
	}
	m.mu.Unlock()
	if ok && s != nil {
		_ = s.Close()
	}
}

// IsCircuitOpen returns whether the circuit is still open (implements CircuitPoolFactory).
func (m *CircuitManager) IsCircuitOpen(circuitID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.activeStreams[circuitID]; !ok {
		return false
	}
	rec, ok := m.store.Get(circuitID)
	return ok && rec != nil && rec.State == protocol.CircuitOpen
}

// ReturnToPool returns a circuit to the pool for reuse (call when SOCKS5 connection ends cleanly).
func (m *CircuitManager) ReturnToPool(circuitID string) {
	if m.pool != nil {
		m.pool.ReturnToPool(circuitID)
	}
}

// MarkCircuitFailed marks a circuit as failed so the pool can replenish (call on error).
func (m *CircuitManager) MarkCircuitFailed(circuitID string) {
	if m.pool != nil {
		m.pool.MarkCircuitFailed(circuitID)
	}
	m.CloseCircuit(circuitID)
}

// CreateDirectExitCircuit creates a direct-exit circuit based on a prepared plan.
func (m *CircuitManager) CreateDirectExitCircuit(plan protocol.PathPlan) (string, error) {
	if len(plan.Hops) != 1 || !plan.Hops[0].IsExit {
		return "", fmt.Errorf("invalid direct-exit plan")
	}
	return m.createCircuitWithPlan(plan)
}

// CreateRelayExitCircuit creates a relay+exit circuit based on a prepared plan.
func (m *CircuitManager) CreateRelayExitCircuit(plan protocol.PathPlan) (string, error) {
	if len(plan.Hops) < 2 {
		return "", fmt.Errorf("relay+exit plan must contain at least 2 hops")
	}
	return m.createCircuitWithPlan(plan)
}

func (m *CircuitManager) createCircuitWithPlan(plan protocol.PathPlan) (string, error) {
	m.mu.Lock()
	id := uuid.NewString()
	now := time.Now()
	rec := &store.CircuitRecord{
		ID:        id,
		State:     protocol.CircuitNew,
		Plan:      plan,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.store.Upsert(rec)
	rec.State = protocol.CircuitCreating
	rec.UpdatedAt = time.Now()
	m.store.Upsert(rec)

	// 連到第一跳（relay 或直連 exit）
	targetPeerID := plan.Hops[0].PeerID
	peerID, err := peer.Decode(targetPeerID)
	if err != nil {
		rec.State = protocol.CircuitClosed
		rec.UpdatedAt = time.Now()
		m.store.Upsert(rec)
		m.mu.Unlock()
		return "", err
	}
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	stream, err := m.host.NewStream(ctx, peerID, protocol.CircuitProtocolID)
	cancel()
	if err != nil {
		rec.State = protocol.CircuitClosed
		rec.UpdatedAt = time.Now()
		m.store.Upsert(rec)
		m.mu.Unlock()
		return "", err
	}
	m.activeStreams[id] = stream
	m.mu.Unlock()

	// 與第一跳密鑰協商
	priv0, pub0, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		stream.Close()
		m.mu.Lock()
		delete(m.activeStreams, id)
		rec.State = protocol.CircuitClosed
		m.store.Upsert(rec)
		m.mu.Unlock()
		return "", err
	}
	initFrame, _ := protocol.NewKeyExchangeInitFrame(id, pub0)
	if err := protocol.WriteFrame(stream, initFrame); err != nil {
		stream.Close()
		m.closeCircuitLocked(id)
		return "", err
	}
	stream.SetReadDeadline(time.Now().Add(15 * time.Second))
	respFrame, err := protocol.ReadFrame(stream)
	stream.SetReadDeadline(time.Time{})
	if err != nil || respFrame.Type != protocol.MsgTypeKeyExchangeResp {
		stream.Close()
		m.closeCircuitLocked(id)
		return "", fmt.Errorf("key exchange with first hop: %w", err)
	}
	var resp protocol.KeyExchangeResp
	if err := json.Unmarshal(respFrame.PayloadJSON, &resp); err != nil || len(resp.Payload) != protocol.X25519KeySize {
		stream.Close()
		m.closeCircuitLocked(id)
		return "", fmt.Errorf("invalid key exchange resp")
	}
	firstSession, err := protocol.NewHopSessionFromKeyExchange(targetPeerID, protocol.HopRoleRelay, priv0, resp.Payload, time.Now().Unix())
	if err != nil {
		stream.Close()
		m.closeCircuitLocked(id)
		return "", err
	}
	if len(plan.Hops) == 1 {
		firstSession.Role = protocol.HopRoleExit
	}

	m.mu.Lock()
	m.hopSessions[id] = []*protocol.HopSession{firstSession}
	m.mu.Unlock()

	// 若為 relay+exit，發送 extend：單層洋蔥（next_hop=exit, inner=客戶端給 exit 的公鑰）
	if len(plan.Hops) > 1 {
		exitPeerID := plan.Hops[plan.ExitHopIndex].PeerID
		privExit, pubExit, err := protocol.GenerateEphemeralKeyPair()
		if err != nil {
			stream.Close()
			m.closeCircuitLocked(id)
			return "", err
		}
		envelope := protocol.OnionEnvelope{
			Header: protocol.RelayLayerHeader{
				NextPeerID:       exitPeerID,
				InnerPayloadType: protocol.InnerPayloadTypeKeyExchange,
				StreamID:         "extend",
			},
			InnerCiphertext: pubExit,
		}
		envelopeBytes, _ := json.Marshal(envelope)
		hops := []*protocol.HopSession{firstSession}
		ciphertext, err := protocol.WrapForward(hops, id, "extend", envelopeBytes)
		if err != nil {
			stream.Close()
			m.closeCircuitLocked(id)
			return "", err
		}
		cell := protocol.OnionCell{Ciphertext: ciphertext}
		payloadJSON, _ := json.Marshal(cell)
		extendFrame := protocol.Frame{
			Type:        protocol.MsgTypeOnionData,
			CircuitID:   id,
			StreamID:    "extend",
			PayloadJSON: payloadJSON,
		}
		if err := protocol.WriteFrame(stream, extendFrame); err != nil {
			stream.Close()
			m.closeCircuitLocked(id)
			return "", err
		}
		stream.SetReadDeadline(time.Now().Add(15 * time.Second))
		backFrame, err := protocol.ReadFrame(stream)
		stream.SetReadDeadline(time.Time{})
		if err != nil || backFrame.Type != protocol.MsgTypeOnionData {
			stream.Close()
			m.closeCircuitLocked(id)
			return "", fmt.Errorf("extend response: %w", err)
		}
		var backCell protocol.OnionCell
		if json.Unmarshal(backFrame.PayloadJSON, &backCell) != nil {
			stream.Close()
			m.closeCircuitLocked(id)
			return "", fmt.Errorf("extend response invalid")
		}
		exitPub, err := protocol.UnwrapBackward(firstSession, id, backFrame.StreamID, backCell.Ciphertext)
		if err != nil || len(exitPub) != protocol.X25519KeySize {
			stream.Close()
			m.closeCircuitLocked(id)
			return "", fmt.Errorf("extend unwrap: %w", err)
		}
		exitSession, err := protocol.NewHopSessionFromKeyExchange(exitPeerID, protocol.HopRoleExit, privExit, exitPub, time.Now().Unix())
		if err != nil {
			stream.Close()
			m.closeCircuitLocked(id)
			return "", err
		}
		m.mu.Lock()
		m.hopSessions[id] = []*protocol.HopSession{firstSession, exitSession}
		m.mu.Unlock()
	}

	m.mu.Lock()
	rec.State = protocol.CircuitOpen
	rec.UpdatedAt = time.Now()
	m.store.Upsert(rec)
	go m.readLoop(id, stream)
	m.mu.Unlock()
	return id, nil
}

func (m *CircuitManager) closeCircuitLocked(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.store.Get(id); ok && rec != nil {
		rec.State = protocol.CircuitClosed
		rec.UpdatedAt = time.Now()
		m.store.Upsert(rec)
	}
	delete(m.activeStreams, id)
	delete(m.hopSessions, id)
}

func (m *CircuitManager) readLoop(circuitID string, s network.Stream) {
	var streamErr error
	defer func() {
		s.Close()
		m.mu.Lock()
		delete(m.activeStreams, circuitID)
		delete(m.hopSessions, circuitID)
		if rec, ok := m.store.Get(circuitID); ok && rec != nil {
			rec.State = protocol.CircuitClosed
			rec.UpdatedAt = time.Now()
			m.store.Upsert(rec)
		}
		m.mu.Unlock()
		// Notify streams with clear error so app sees "circuit stream closed" instead of generic reset
		if streamErr != nil && streamErr != io.EOF {
			m.streams.NotifyCircuitClosed(circuitID, fmt.Errorf("circuit broken: %w", streamErr))
		} else {
			m.streams.NotifyCircuitClosed(circuitID, nil)
		}
	}()
	for {
		frame, err := protocol.ReadFrame(s)
		if err != nil {
			streamErr = err
			return
		}
		switch frame.Type {
		case protocol.MsgTypeConnected:
			var msg protocol.Connected
			if err := json.Unmarshal(frame.PayloadJSON, &msg); err == nil {
				m.mu.Lock()
				if ch, ok := m.pendingConnected[frame.StreamID]; ok {
					delete(m.pendingConnected, frame.StreamID)
					ch <- msg
				}
				m.mu.Unlock()
			}

		case protocol.MsgTypeOnionData:
			var cell protocol.OnionCell
			if err := json.Unmarshal(frame.PayloadJSON, &cell); err != nil {
				continue
			}
			hops := m.getHopSessions(circuitID)
			data := cell.Ciphertext
			for _, hop := range hops {
				var unwrapErr error
				data, unwrapErr = protocol.UnwrapBackward(hop, frame.CircuitID, frame.StreamID, data)
				if unwrapErr != nil {
					continue
				}
			}
			var payload protocol.OnionPayload
			if json.Unmarshal(data, &payload) != nil {
				continue
			}
			if payload.Kind == "connected" && payload.Connected != nil {
				m.mu.Lock()
				if ch, ok := m.pendingConnected[frame.StreamID]; ok {
					delete(m.pendingConnected, frame.StreamID)
					ch <- *payload.Connected
				}
				m.mu.Unlock()
			} else if payload.Kind == "data" && payload.Data != nil {
				if stream, ok := m.streams.Get(frame.StreamID); ok {
					_, _ = stream.Conn.Write(payload.Data.Payload)
				}
			}
		case protocol.MsgTypeData, protocol.MsgTypeEnd:
			if stream, ok := m.streams.Get(frame.StreamID); ok {
				if frame.Type == protocol.MsgTypeData {
					var cell protocol.DataCell
					if err := json.Unmarshal(frame.PayloadJSON, &cell); err == nil {
						_, _ = stream.Conn.Write(cell.Payload)
					}
				} else {
					m.streams.Remove(frame.StreamID)
				}
			}
		default:
			// 其他類型暫時忽略
		}
	}
}

// BeginTCP sends a BEGIN_TCP request over the given circuit and waits for a
// CONNECTED response from the exit (or next hop). It returns error on failure.
func (m *CircuitManager) BeginTCP(circuitID, streamID, host string, port int) error {
	m.mu.Lock()
	s, ok := m.activeStreams[circuitID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no active stream for circuit %s", circuitID)
	}
	ch := make(chan protocol.Connected, 1)
	m.pendingConnected[streamID] = ch
	m.mu.Unlock()

	hops := m.getHopSessions(circuitID)
	if len(hops) == 0 {
		return fmt.Errorf("no hop sessions for circuit %s", circuitID)
	}
	payload := protocol.OnionPayload{
		Kind:  "begin",
		Begin: &protocol.BeginTCP{TargetHost: host, TargetPort: port},
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ciphertext, err := protocol.WrapForward(hops, circuitID, streamID, payloadJSON)
	if err != nil {
		return err
	}
	cell := protocol.OnionCell{Ciphertext: ciphertext}
	frame, err := protocol.NewOnionDataFrame(circuitID, streamID, cell)
	if err != nil {
		return err
	}
	if err := protocol.WriteFrame(s, frame); err != nil {
		return err
	}

	// Wait for CONNECTED with timeout.
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()
	select {
	case resp := <-ch:
		if !resp.OK {
			if resp.Error != "" {
				return fmt.Errorf("remote connect failed: %s", resp.Error)
			}
			return fmt.Errorf("remote connect failed")
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for CONNECTED timed out")
	}
}

// StartDataPump starts a goroutine that reads from the local conn and sends
// DATA frames over the given circuit/stream until EOF or error.
func (m *CircuitManager) StartDataPump(circuitID, streamID string, conn net.Conn) {
	m.mu.Lock()
	s, ok := m.activeStreams[circuitID]
	m.mu.Unlock()
	if !ok {
		return
	}

	hops := m.getHopSessions(circuitID)
	if len(hops) == 0 {
		return
	}
	go func() {
		buf := make([]byte, 16*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				payload := protocol.OnionPayload{
					Kind: "data",
					Data: &protocol.DataCell{Payload: buf[:n]},
				}
				payloadJSON, encErr := json.Marshal(payload)
				if encErr != nil {
					return
				}
				ciphertext, encErr := protocol.WrapForward(hops, circuitID, streamID, payloadJSON)
				if encErr != nil {
					return
				}
				cell := protocol.OnionCell{Ciphertext: ciphertext}
				frame, encErr := protocol.NewOnionDataFrame(circuitID, streamID, cell)
				if encErr != nil {
					return
				}
				if err := protocol.WriteFrame(s, frame); err != nil {
					return
				}
			}
			if err != nil {
				endFrame, encErr := protocol.NewEndFrame(circuitID, streamID, "local_closed")
				if encErr == nil {
					_ = protocol.WriteFrame(s, endFrame)
				}
				return
			}
		}
	}()
}


