package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/store"
)

const (
	heartbeatStreamID        = "__heartbeat__"
	poolAcquireRetryInterval = 100 * time.Millisecond
	poolAcquireRetryAttempts = 50
	circuitCreateTimeout     = 30 * time.Second
)

type BeginErrorKind string

const (
	BeginErrorCircuit BeginErrorKind = "circuit"
	BeginErrorRemote  BeginErrorKind = "remote"
	BeginErrorTimeout BeginErrorKind = "timeout"
)

// BeginError classifies failures while opening a remote stream over an existing circuit.
type BeginError struct {
	Kind    BeginErrorKind
	Message string
}

func (e *BeginError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// HeartbeatConfig controls client-side circuit ping/pong probing.
type HeartbeatConfig struct {
	Enabled           bool
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  int
	SkipWhenActiveFor time.Duration
	IdleMin           time.Duration
	IdleMax           time.Duration
	PollInterval      time.Duration
}

// CircuitManager manages circuit lifecycle and delegates storage to CircuitStore.
type CircuitManager struct {
	mu              sync.Mutex
	ctx             context.Context
	host            host.Host
	store           *store.CircuitStore
	pathSelector    *PathSelector
	streams         *StreamManager
	pool            *CircuitPool
	buildRetries    int // 1~2: retries when building circuit fails (relay/exit failover)
	beginTCPRetries int // 1~2: retries when exit connect (BEGIN_TCP) fails
	// activeStreams keeps the libp2p streams per circuit.
	activeStreams map[string]network.Stream
	// hopSessions 每條 circuit 的逐跳會話（順序 [relay, exit]），用於洋蔥加解密；不暴露給 API。
	hopSessions map[string][]*protocol.HopSession
	// pendingConnected stores waiting channels for CONNECTED messages, keyed by streamID.
	pendingConnected map[string]chan protocol.Connected
	// pendingPongs stores waiting channels for heartbeat pong messages, keyed by pingID.
	pendingPongs map[string]chan protocol.Pong
	heartbeat    HeartbeatConfig
	beginTimeout time.Duration
	heartbeatDue map[string]time.Time
	rng          *rand.Rand
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
		pendingPongs:     make(map[string]chan protocol.Pong),
		beginTimeout:     20 * time.Second,
		heartbeatDue:     make(map[string]time.Time),
		rng:              rand.New(rand.NewSource(time.Now().UnixNano())),
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

// SetBeginConnectTimeout updates the timeout used while waiting for CONNECTED after BEGIN.
func (m *CircuitManager) SetBeginConnectTimeout(timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	m.beginTimeout = timeout
}

// SetHeartbeatConfig updates circuit heartbeat settings.
func (m *CircuitManager) SetHeartbeatConfig(cfg HeartbeatConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 8 * time.Second
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.IdleMin <= 0 {
		cfg.IdleMin = 1500 * time.Millisecond
	}
	if cfg.IdleMax <= 0 {
		cfg.IdleMax = 9500 * time.Millisecond
	}
	if cfg.IdleMax < cfg.IdleMin {
		cfg.IdleMax = cfg.IdleMin
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.SkipWhenActiveFor < 0 {
		cfg.SkipWhenActiveFor = 0
	} else if cfg.SkipWhenActiveFor == 0 {
		cfg.SkipWhenActiveFor = 30 * time.Second
	}
	m.heartbeat = cfg
}

// StartHeartbeatLoop starts periodic circuit ping/pong health checks.
func (m *CircuitManager) StartHeartbeatLoop(ctx context.Context) {
	m.mu.Lock()
	cfg := m.heartbeat
	m.mu.Unlock()
	if !cfg.Enabled {
		return
	}
	go m.runHeartbeatLoop(ctx)
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

// GetPoolConfig returns the current circuit pool configuration.
func (m *CircuitManager) GetPoolConfig() *config.CircuitPoolConfig {
	m.mu.Lock()
	pool := m.pool
	m.mu.Unlock()
	if pool == nil {
		return nil
	}
	cfg := pool.GetConfig()
	return &cfg
}

// SetPoolTotalLimits updates the runtime total circuit pool bounds.
func (m *CircuitManager) SetPoolTotalLimits(minTotal, maxTotal int) bool {
	m.mu.Lock()
	pool := m.pool
	m.mu.Unlock()
	if pool == nil {
		return false
	}
	pool.SetTotalLimits(minTotal, maxTotal)
	return true
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
// Multiple streams can share the same circuit; use ReturnToPool when each stream ends.
func (m *CircuitManager) EnsureCircuitFromPool(kind PoolKind) (string, error) {
	m.mu.Lock()
	pool := m.pool
	m.mu.Unlock()
	return m.ensureCircuitFromPoolExcluding(pool, kind, nil)
}

// EnsureCircuitFromPoolExcluding gets a circuit from the pool or creates one on demand,
// while excluding the provided peer IDs during any new path selection.
func (m *CircuitManager) EnsureCircuitFromPoolExcluding(kind PoolKind, excludePeerIDs []string) (string, error) {
	m.mu.Lock()
	pool := m.pool
	m.mu.Unlock()
	return m.ensureCircuitFromPoolExcluding(pool, kind, excludePeerIDs)
}

func (m *CircuitManager) ensureCircuitFromPoolExcluding(pool *CircuitPool, kind PoolKind, excludePeerIDs []string) (string, error) {
	if pool == nil {
		return m.CreateForPoolExcluding(kind, excludePeerIDs)
	}
	for attempt := 0; attempt < poolAcquireRetryAttempts; attempt++ {
		if id, ok := pool.GetFromPool(kind); ok {
			return id, nil
		}
		if pool.TryReserveBuild(kind) {
			id, err := m.CreateForPoolExcluding(kind, excludePeerIDs)
			if err != nil {
				pool.CancelReservedBuild(kind)
				return "", err
			}
			pool.RegisterCircuitInUse(kind, id)
			return id, nil
		}
		time.Sleep(poolAcquireRetryInterval)
	}
	return "", fmt.Errorf("circuit pool is at capacity")
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
	delete(m.hopSessions, circuitID)
	delete(m.heartbeatDue, circuitID)
	m.mu.Unlock()
	if m.store != nil {
		m.store.Delete(circuitID)
	}
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
	return ok && rec != nil && rec.State == protocol.CircuitOpen && rec.Alive
}

// CircuitReuseScore returns the reuse preference score for a circuit.
func (m *CircuitManager) CircuitReuseScore(circuitID string) int {
	if m.store == nil {
		return 0
	}
	rec, ok := m.store.Get(circuitID)
	if !ok || rec == nil {
		return 0
	}
	return rec.HeartbeatSuccesses
}

// ReturnToPool returns a circuit to the pool for reuse (call when SOCKS5 connection ends cleanly).
func (m *CircuitManager) ReturnToPool(circuitID string) {
	if m.pool != nil {
		m.pool.ReturnToPool(circuitID)
	}
}

// RegisterCircuitInUse registers a circuit created outside EnsureCircuitFromPool so it can be returned to the pool later.
func (m *CircuitManager) RegisterCircuitInUse(kind PoolKind, circuitID string) {
	if m.pool != nil {
		m.pool.RegisterCircuitInUse(kind, circuitID)
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
	createDeadline := now.Add(circuitCreateTimeout)
	rec := &store.CircuitRecord{
		ID:            id,
		State:         protocol.CircuitNew,
		Plan:          plan,
		CreatedAt:     now,
		UpdatedAt:     now,
		LastTrafficAt: now,
		Alive:         true,
	}
	m.store.Upsert(rec)
	rec.State = protocol.CircuitCreating
	rec.UpdatedAt = time.Now()
	rec.Alive = true
	m.store.Upsert(rec)

	// 連到第一跳（relay 或直連 exit）
	targetPeerID := plan.Hops[0].PeerID
	peerID, err := peer.Decode(targetPeerID)
	if err != nil {
		m.mu.Unlock()
		m.store.Delete(id)
		return "", err
	}
	newStreamTimeout := time.Until(createDeadline)
	if newStreamTimeout <= 0 {
		m.mu.Unlock()
		m.store.Delete(id)
		return "", fmt.Errorf("circuit create timeout")
	}
	ctx, cancel := context.WithTimeout(m.ctx, newStreamTimeout)
	stream, err := m.host.NewStream(ctx, peerID, protocol.CircuitProtocolID)
	cancel()
	if err != nil {
		m.mu.Unlock()
		m.store.Delete(id)
		return "", err
	}
	_ = stream.SetDeadline(createDeadline)
	m.activeStreams[id] = stream
	m.mu.Unlock()

	// 與第一跳密鑰協商
	priv0, pub0, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		stream.Close()
		m.mu.Lock()
		delete(m.activeStreams, id)
		m.mu.Unlock()
		m.store.Delete(id)
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
	rec.Alive = true
	m.store.Upsert(rec)
	m.heartbeatDue[id] = time.Now().Add(m.nextHeartbeatDelayLocked())
	go m.readLoop(id, stream)
	m.mu.Unlock()
	_ = stream.SetDeadline(time.Time{})
	return id, nil
}

func (m *CircuitManager) closeCircuitLocked(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.activeStreams, id)
	delete(m.hopSessions, id)
	delete(m.heartbeatDue, id)
	if m.store != nil {
		m.store.Delete(id)
	}
}

func (m *CircuitManager) readLoop(circuitID string, s network.Stream) {
	var streamErr error
	defer func() {
		s.Close()
		m.mu.Lock()
		delete(m.activeStreams, circuitID)
		delete(m.hopSessions, circuitID)
		delete(m.heartbeatDue, circuitID)
		m.mu.Unlock()
		if m.store != nil {
			m.store.Delete(circuitID)
		}
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
			if payload.Kind == protocol.OnionPayloadKindConnected && payload.Connected != nil {
				m.mu.Lock()
				if ch, ok := m.pendingConnected[frame.StreamID]; ok {
					delete(m.pendingConnected, frame.StreamID)
					ch <- *payload.Connected
				}
				m.mu.Unlock()
			} else if payload.Kind == protocol.OnionPayloadKindPong && payload.Pong != nil {
				m.mu.Lock()
				if ch, ok := m.pendingPongs[payload.Pong.PingID]; ok {
					delete(m.pendingPongs, payload.Pong.PingID)
					ch <- *payload.Pong
				}
				m.mu.Unlock()
			} else if payload.Kind == protocol.OnionPayloadKindData && payload.Data != nil {
				if stream, ok := m.streams.Get(frame.StreamID); ok && stream.Conn != nil {
					n, _ := stream.Conn.Write(payload.Data.Payload)
					if n > 0 {
						m.addCircuitBytes(circuitID, 0, uint64(n))
					}
				}
			}
		case protocol.MsgTypeData, protocol.MsgTypeEnd:
			if stream, ok := m.streams.Get(frame.StreamID); ok {
				if frame.Type == protocol.MsgTypeData {
					var cell protocol.DataCell
					if err := json.Unmarshal(frame.PayloadJSON, &cell); err == nil && stream.Conn != nil {
						n, _ := stream.Conn.Write(cell.Payload)
						if n > 0 {
							m.addCircuitBytes(circuitID, 0, uint64(n))
						}
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
		return &BeginError{Kind: BeginErrorCircuit, Message: fmt.Sprintf("no active stream for circuit %s", circuitID)}
	}
	ch := make(chan protocol.Connected, 1)
	m.pendingConnected[streamID] = ch
	beginTimeout := m.beginTimeout
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.pendingConnected, streamID)
		m.mu.Unlock()
	}()

	hops := m.getHopSessions(circuitID)
	if len(hops) == 0 {
		return &BeginError{Kind: BeginErrorCircuit, Message: fmt.Sprintf("no hop sessions for circuit %s", circuitID)}
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
		return &BeginError{Kind: BeginErrorCircuit, Message: err.Error()}
	}

	// Wait for CONNECTED with timeout.
	ctx, cancel := context.WithTimeout(m.ctx, beginTimeout)
	defer cancel()
	select {
	case resp := <-ch:
		if !resp.OK {
			if resp.Error != "" {
				return &BeginError{Kind: BeginErrorRemote, Message: fmt.Sprintf("remote connect failed: %s", resp.Error)}
			}
			return &BeginError{Kind: BeginErrorRemote, Message: "remote connect failed"}
		}
		m.touchCircuitActivity(circuitID)
		return nil
	case <-ctx.Done():
		m.markCircuitTimeout(circuitID)
		return &BeginError{Kind: BeginErrorTimeout, Message: "wait for CONNECTED timed out"}
	}
}

// BeginUDP sends a BEGIN_UDP request over the given circuit and waits for a
// CONNECTED response from the exit. It returns error on failure.
func (m *CircuitManager) BeginUDP(circuitID, streamID, host string, port int) error {
	m.mu.Lock()
	s, ok := m.activeStreams[circuitID]
	if !ok {
		m.mu.Unlock()
		return &BeginError{Kind: BeginErrorCircuit, Message: fmt.Sprintf("no active stream for circuit %s", circuitID)}
	}
	ch := make(chan protocol.Connected, 1)
	m.pendingConnected[streamID] = ch
	beginTimeout := m.beginTimeout
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.pendingConnected, streamID)
		m.mu.Unlock()
	}()

	hops := m.getHopSessions(circuitID)
	if len(hops) == 0 {
		return &BeginError{Kind: BeginErrorCircuit, Message: fmt.Sprintf("no hop sessions for circuit %s", circuitID)}
	}
	payload := protocol.OnionPayload{
		Kind:     "begin_udp",
		BeginUDP: &protocol.BeginUDP{TargetHost: host, TargetPort: port},
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
		return &BeginError{Kind: BeginErrorCircuit, Message: err.Error()}
	}

	ctx, cancel := context.WithTimeout(m.ctx, beginTimeout)
	defer cancel()
	select {
	case resp := <-ch:
		if !resp.OK {
			if resp.Error != "" {
				return &BeginError{Kind: BeginErrorRemote, Message: fmt.Sprintf("remote udp connect failed: %s", resp.Error)}
			}
			return &BeginError{Kind: BeginErrorRemote, Message: "remote udp connect failed"}
		}
		m.touchCircuitActivity(circuitID)
		return nil
	case <-ctx.Done():
		m.markCircuitTimeout(circuitID)
		return &BeginError{Kind: BeginErrorTimeout, Message: "wait for CONNECTED (udp) timed out"}
	}
}

// SendData sends a DATA frame over the given circuit/stream (used for UDP payloads).
func (m *CircuitManager) SendData(circuitID, streamID string, payload []byte) error {
	m.mu.Lock()
	s, ok := m.activeStreams[circuitID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no active stream for circuit %s", circuitID)
	}
	hops := m.getHopSessions(circuitID)
	if len(hops) == 0 {
		return fmt.Errorf("no hop sessions for circuit %s", circuitID)
	}
	payloadCopy := append([]byte(nil), payload...)
	pl := protocol.OnionPayload{Kind: "data", Data: &protocol.DataCell{Payload: payloadCopy}}
	payloadJSON, err := json.Marshal(pl)
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
	if len(payload) > 0 {
		m.addCircuitBytes(circuitID, uint64(len(payload)), 0)
	}
	return nil
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
				m.addCircuitBytes(circuitID, uint64(n), 0)
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

func (m *CircuitManager) addCircuitBytes(circuitID string, sent, recv uint64) {
	if m.store == nil {
		return
	}
	m.store.AddStats(circuitID, sent, recv)
	m.scheduleHeartbeat(circuitID)
}

func (m *CircuitManager) touchCircuitActivity(circuitID string) {
	if m.store == nil {
		return
	}
	rec, ok := m.store.Get(circuitID)
	if !ok || rec == nil {
		return
	}
	rec.LastTrafficAt = time.Now()
	rec.UpdatedAt = time.Now()
	rec.Alive = true
	rec.ConsecutiveFailures = 0
	m.store.Upsert(rec)
	m.scheduleHeartbeat(circuitID)
}

// CanReuseCircuitAfterBeginError reports whether a circuit should be kept after a BEGIN failure.
func (m *CircuitManager) CanReuseCircuitAfterBeginError(circuitID string, err error) bool {
	if !m.IsCircuitOpen(circuitID) {
		return false
	}
	var beginErr *BeginError
	if !errors.As(err, &beginErr) {
		return false
	}
	return beginErr.Kind == BeginErrorRemote || beginErr.Kind == BeginErrorTimeout
}

func (m *CircuitManager) runHeartbeatLoop(ctx context.Context) {
	m.mu.Lock()
	cfg := m.heartbeat
	m.mu.Unlock()
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, circuitID := range m.heartbeatCandidates(time.Now()) {
				m.probeCircuit(circuitID)
			}
		}
	}
}

func (m *CircuitManager) heartbeatCandidates(now time.Time) []string {
	m.mu.Lock()
	ids := make([]string, 0, len(m.activeStreams))
	for circuitID := range m.activeStreams {
		due, ok := m.heartbeatDue[circuitID]
		if ok && !now.Before(due) {
			ids = append(ids, circuitID)
		}
	}
	m.mu.Unlock()

	out := make([]string, 0, len(ids))
	for _, circuitID := range ids {
		rec, ok := m.store.Get(circuitID)
		if !ok || rec == nil || rec.State != protocol.CircuitOpen {
			continue
		}
		out = append(out, circuitID)
	}
	return out
}

func (m *CircuitManager) probeCircuit(circuitID string) {
	m.mu.Lock()
	due, ok := m.heartbeatDue[circuitID]
	m.mu.Unlock()
	if !ok || time.Now().Before(due) {
		return
	}
	lastPing := time.Now()
	pong, err := m.sendHeartbeat(circuitID, lastPing)
	if err != nil {
		m.handleHeartbeatFailure(circuitID, lastPing)
		return
	}
	m.handleHeartbeatSuccess(circuitID, lastPing, pong)
}

func (m *CircuitManager) sendHeartbeat(circuitID string, sentAt time.Time) (protocol.Pong, error) {
	m.mu.Lock()
	s, ok := m.activeStreams[circuitID]
	cfg := m.heartbeat
	m.mu.Unlock()
	if !ok {
		return protocol.Pong{}, fmt.Errorf("no active stream for circuit %s", circuitID)
	}
	hops := m.getHopSessions(circuitID)
	if len(hops) == 0 {
		return protocol.Pong{}, fmt.Errorf("no hop sessions for circuit %s", circuitID)
	}

	pingID := uuid.NewString()
	ch := make(chan protocol.Pong, 1)
	m.mu.Lock()
	m.pendingPongs[pingID] = ch
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.pendingPongs, pingID)
		m.mu.Unlock()
	}()

	payload := protocol.OnionPayload{
		Kind: protocol.OnionPayloadKindPing,
		Ping: &protocol.Ping{
			CircuitID:  circuitID,
			PingID:     pingID,
			SentAtUnix: sentAt.UnixMilli(),
		},
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return protocol.Pong{}, err
	}
	ciphertext, err := protocol.WrapForward(hops, circuitID, heartbeatStreamID, payloadJSON)
	if err != nil {
		return protocol.Pong{}, err
	}
	cell := protocol.OnionCell{Ciphertext: ciphertext}
	frame, err := protocol.NewOnionDataFrame(circuitID, heartbeatStreamID, cell)
	if err != nil {
		return protocol.Pong{}, err
	}
	if err := protocol.WriteFrame(s, frame); err != nil {
		return protocol.Pong{}, err
	}
	log.Printf("[heartbeat] heartbeat_ping_sent circuit=%s ping=%s", circuitID, pingID)

	timer := time.NewTimer(cfg.Timeout)
	defer timer.Stop()
	select {
	case pong := <-ch:
		return pong, nil
	case <-timer.C:
		return protocol.Pong{}, fmt.Errorf("heartbeat timeout")
	case <-m.ctx.Done():
		return protocol.Pong{}, m.ctx.Err()
	}
}

func (m *CircuitManager) handleHeartbeatSuccess(circuitID string, lastPing time.Time, pong protocol.Pong) {
	rec, ok := m.store.Get(circuitID)
	if !ok || rec == nil {
		return
	}
	lastPong := time.Now()
	if pong.EchoAtUnix > 0 {
		lastPong = time.UnixMilli(pong.EchoAtUnix)
	}
	rtt := time.Since(lastPing)
	smoothed := rtt
	if rec.SmoothedRTT > 0 {
		smoothed = time.Duration(float64(rec.SmoothedRTT)*0.8 + float64(rtt)*0.2)
	}
	recovered := !rec.Alive
	m.store.SetHealth(circuitID, true, lastPing, lastPong, 0, rec.HeartbeatSuccesses+1, smoothed)
	m.scheduleHeartbeat(circuitID)
	log.Printf("[heartbeat] heartbeat_pong_received circuit=%s ping=%s rtt_ms=%.1f", circuitID, pong.PingID, float64(rtt.Nanoseconds())/1e6)
	if recovered {
		log.Printf("[heartbeat] heartbeat_circuit_recovered circuit=%s", circuitID)
	}
}

func (m *CircuitManager) handleHeartbeatFailure(circuitID string, lastPing time.Time) {
	rec, ok := m.store.Get(circuitID)
	if !ok || rec == nil {
		return
	}
	m.mu.Lock()
	cfg := m.heartbeat
	pool := m.pool
	m.mu.Unlock()

	failures := rec.ConsecutiveFailures + 1
	alive := true
	if failures >= cfg.FailureThreshold {
		alive = false
	}
	m.store.SetHealth(circuitID, alive, lastPing, rec.LastPongAt, failures, rec.HeartbeatSuccesses, rec.SmoothedRTT)
	log.Printf("[heartbeat] heartbeat_timeout circuit=%s failures=%d", circuitID, failures)
	if alive {
		m.scheduleHeartbeat(circuitID)
		return
	}
	log.Printf("[heartbeat] heartbeat_circuit_unhealthy circuit=%s", circuitID)
	if pool != nil {
		pool.MarkCircuitFailed(circuitID)
	}
	m.CloseCircuit(circuitID)
}

func (m *CircuitManager) markCircuitTimeout(circuitID string) {
	if m.store == nil {
		return
	}
	rec, ok := m.store.Get(circuitID)
	if !ok || rec == nil {
		return
	}
	m.mu.Lock()
	threshold := m.heartbeat.FailureThreshold
	m.mu.Unlock()
	if threshold <= 0 {
		threshold = 5
	}
	rec.ConsecutiveFailures++
	rec.Alive = rec.ConsecutiveFailures < threshold
	rec.UpdatedAt = time.Now()
	m.store.Upsert(rec)
}

func (m *CircuitManager) scheduleHeartbeat(circuitID string) {
	if circuitID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.heartbeatDue[circuitID] = time.Now().Add(m.nextHeartbeatDelayLocked())
}

func (m *CircuitManager) nextHeartbeatDelayLocked() time.Duration {
	cfg := m.heartbeat
	if cfg.IdleMin <= 0 {
		cfg.IdleMin = 1500 * time.Millisecond
	}
	if cfg.IdleMax <= 0 {
		cfg.IdleMax = 9500 * time.Millisecond
	}
	if cfg.IdleMax <= cfg.IdleMin {
		return cfg.IdleMin
	}
	delta := cfg.IdleMax - cfg.IdleMin
	return cfg.IdleMin + time.Duration(m.rng.Int63n(int64(delta)))
}
