package api

import (
	"sync"
	"time"
)

// ErrorEntry is a single recent error for API.
type ErrorEntry struct {
	At       time.Time `json:"at"`
	Message  string    `json:"message"`
	Category string    `json:"category"` // e.g. "socks5", "circuit", "pool"
}

// RecentErrorsStore keeps a bounded list of recent errors for observability.
type RecentErrorsStore struct {
	mu   sync.Mutex
	errs []ErrorEntry
	max  int
}

// NewRecentErrorsStore creates a store that keeps at most max entries.
func NewRecentErrorsStore(max int) *RecentErrorsStore {
	if max <= 0 {
		max = 100
	}
	return &RecentErrorsStore{errs: make([]ErrorEntry, 0, max), max: max}
}

// Add records an error (category e.g. "socks5", "circuit", "pool").
func (s *RecentErrorsStore) Add(category, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs = append(s.errs, ErrorEntry{At: time.Now(), Message: message, Category: category})
	if len(s.errs) > s.max {
		s.errs = s.errs[len(s.errs)-s.max:]
	}
}

// GetRecent returns a copy of recent errors (newest last).
func (s *RecentErrorsStore) GetRecent() []ErrorEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ErrorEntry, len(s.errs))
	copy(out, s.errs)
	return out
}
