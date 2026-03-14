package store

import (
	"sync"
	"time"

	"github.com/chenjia404/meshproxy/internal/protocol"
)

// CircuitRecord stores mutable information about a circuit.
type CircuitRecord struct {
	ID        string
	State     protocol.CircuitState
	Plan      protocol.PathPlan
	// Keys holds per-hop forward/backward keys for this circuit. It will
	// remain nil until key negotiation is implemented.
	Keys      *protocol.CircuitKeys
	CreatedAt time.Time
	UpdatedAt time.Time
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

// GetAll returns a snapshot of all circuits as CircuitInfo.
func (s *CircuitStore) GetAll() []protocol.CircuitInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]protocol.CircuitInfo, 0, len(s.circuits))
	for _, rec := range s.circuits {
		out = append(out, protocol.CircuitInfo{
			ID:        rec.ID,
			State:     rec.State,
			Plan:      rec.Plan,
			CreatedAt: rec.CreatedAt,
			UpdatedAt: rec.UpdatedAt,
		})
	}
	return out
}

