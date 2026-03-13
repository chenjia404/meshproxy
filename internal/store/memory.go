package store

import "sync"

// MemoryStore is a simple in-memory key-value store used as a placeholder
// for future state management.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryStore creates a new empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		data: make(map[string][]byte),
	}
}

// Get returns the value associated with the given key and whether it exists.
func (s *MemoryStore) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// Set stores the given key and value.
func (s *MemoryStore) Set(key string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

