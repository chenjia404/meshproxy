package store

import (
	"sync"
	"time"

	"github.com/chenjia404/meshproxy/internal/protocol"
)

// CircuitRecord stores mutable information about a circuit.
type CircuitRecord struct {
	ID    string
	State protocol.CircuitState
	Plan  protocol.PathPlan
	// Keys holds per-hop forward/backward keys for this circuit. It will
	// remain nil until key negotiation is implemented.
	Keys                *protocol.CircuitKeys
	CreatedAt           time.Time
	UpdatedAt           time.Time
	BytesSent           uint64
	BytesReceived       uint64
	LastTrafficAt       time.Time
	LastPingAt          time.Time
	LastPongAt          time.Time
	ConsecutiveFailures int
	SmoothedRTT         time.Duration
	Alive               bool
}

// CircuitStore keeps track of all circuits in memory.
type CircuitStore struct {
	mu       sync.RWMutex
	circuits map[string]*CircuitRecord
}

// NewCircuitStore creates a new empty CircuitStore.
func NewCircuitStore() *CircuitStore {
	return &CircuitStore{
		circuits: make(map[string]*CircuitRecord),
	}
}

// Upsert inserts or updates a circuit record.
func (s *CircuitStore) Upsert(rec *CircuitRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.circuits[rec.ID] = rec
}

// Get returns the circuit record by ID, or nil and false if not found.
func (s *CircuitStore) Get(id string) (*CircuitRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.circuits[id]
	return rec, ok
}

// Delete removes a circuit record by ID.
func (s *CircuitStore) Delete(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.circuits, id)
}

// GetAll returns a snapshot of all circuits as CircuitInfo.
func (s *CircuitStore) GetAll() []protocol.CircuitInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]protocol.CircuitInfo, 0, len(s.circuits))
	for _, rec := range s.circuits {
		out = append(out, protocol.CircuitInfo{
			ID:                  rec.ID,
			State:               rec.State,
			Plan:                rec.Plan,
			CreatedAt:           rec.CreatedAt,
			UpdatedAt:           rec.UpdatedAt,
			BytesSent:           rec.BytesSent,
			BytesReceived:       rec.BytesReceived,
			LastPingAt:          rec.LastPingAt,
			LastPongAt:          rec.LastPongAt,
			ConsecutiveFailures: rec.ConsecutiveFailures,
			SmoothedRTTMillis:   float64(rec.SmoothedRTT.Nanoseconds()) / 1e6,
			Alive:               rec.Alive,
		})
	}
	return out
}

// AddStats increments the stored bytes for the given circuit.
func (s *CircuitStore) AddStats(id string, sent, recv uint64) {
	if id == "" || (sent == 0 && recv == 0) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.circuits[id]; ok && rec != nil {
		rec.BytesSent += sent
		rec.BytesReceived += recv
		rec.LastTrafficAt = time.Now()
		rec.UpdatedAt = time.Now()
	}
}

// SetHealth updates heartbeat-related metadata for the given circuit.
func (s *CircuitStore) SetHealth(id string, alive bool, lastPing, lastPong time.Time, consecutiveFailures int, smoothedRTT time.Duration) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.circuits[id]; ok && rec != nil {
		rec.Alive = alive
		rec.LastPingAt = lastPing
		rec.LastPongAt = lastPong
		rec.ConsecutiveFailures = consecutiveFailures
		rec.SmoothedRTT = smoothedRTT
		rec.UpdatedAt = time.Now()
	}
}

// Count returns the number of circuit records currently stored.
func (s *CircuitStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.circuits)
}
