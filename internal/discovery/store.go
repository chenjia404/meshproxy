package discovery

import "sync"

// Store keeps a cache of known node descriptors.
type Store struct {
	mu    sync.RWMutex
	nodes map[string]*NodeDescriptor // key: peer_id
}

// NewStore creates a new empty discovery store.
func NewStore() *Store {
	return &Store{
		nodes: make(map[string]*NodeDescriptor),
	}
}

// Upsert inserts or updates a descriptor.
func (s *Store) Upsert(d *NodeDescriptor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes[d.PeerID] = d
}

// GetAll returns all known descriptors.
func (s *Store) GetAll() []*NodeDescriptor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*NodeDescriptor, 0, len(s.nodes))
	for _, d := range s.nodes {
		out = append(out, d)
	}
	return out
}

// ListRelays returns descriptors that can act as relay.
func (s *Store) ListRelays() []*NodeDescriptor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*NodeDescriptor
	for _, d := range s.nodes {
		if d.Relay {
			out = append(out, d)
		}
	}
	return out
}

// ListExits returns descriptors that can act as exit.
func (s *Store) ListExits() []*NodeDescriptor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*NodeDescriptor
	for _, d := range s.nodes {
		if d.Exit {
			out = append(out, d)
		}
	}
	return out
}

