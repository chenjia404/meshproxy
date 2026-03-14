package relaycache

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"
)

// Record stores a relay peer ID with associated multiaddrs.
type Record struct {
	PeerID string   `json:"peer_id"`
	Addrs  []string `json:"addrs"`
	SeenAt int64    `json:"seen_at,omitempty"`
}

// Cache persists relay records to a JSON file.
type Cache struct {
	mu      sync.Mutex
	path    string
	records map[string]*Record
}

// New creates a cache backed by the given file. The directory containing the file
// must already exist.
func New(path string) (*Cache, error) {
	c := &Cache{
		path:    path,
		records: make(map[string]*Record),
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

// Clear removes all cached records and flushes an empty snapshot to disk.
func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = make(map[string]*Record)
	return c.persistLocked()
}

// Add stores the provided addrs for the given peer ID. If new data is appended,
// the cache is flushed back to disk.
func (c *Cache) Add(peerID string, addrs []string) error {
	return c.AddWithLimit(peerID, addrs, 0)
}

// AddWithLimit stores the provided addrs for the given peer ID and trims the
// cache to at most limit peer records when limit > 0.
func (c *Cache) AddWithLimit(peerID string, addrs []string, limit int) error {
	if peerID == "" || len(addrs) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	rec, ok := c.records[peerID]
	if !ok {
		rec = &Record{PeerID: peerID}
		c.records[peerID] = rec
	}
	rec.SeenAt = time.Now().Unix()

	existing := make(map[string]struct{}, len(rec.Addrs))
	for _, a := range rec.Addrs {
		existing[a] = struct{}{}
	}

	changed := false
	for _, addr := range addrs {
		if addr == "" {
			continue
		}
		if _, seen := existing[addr]; seen {
			continue
		}
		rec.Addrs = append(rec.Addrs, addr)
		existing[addr] = struct{}{}
		changed = true
	}
	if !ok && len(rec.Addrs) > 0 {
		changed = true
	}
	if limit > 0 && len(c.records) > limit {
		c.trimLocked(limit)
		changed = true
	}

	if changed {
		return c.persistLocked()
	}
	return nil
}

// Addrs returns all unique addresses stored in the cache.
func (c *Cache) Addrs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := make(map[string]struct{})
	out := make([]string, 0, len(c.records))
	for _, rec := range c.records {
		for _, addr := range rec.Addrs {
			if addr == "" {
				continue
			}
			if _, ok := seen[addr]; ok {
				continue
			}
			seen[addr] = struct{}{}
			out = append(out, addr)
		}
	}
	sort.Strings(out)
	return out
}

// Records returns a sorted snapshot of stored records.
func (c *Cache) Records() []*Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*Record, 0, len(c.records))
	for _, rec := range c.records {
		addrs := append([]string(nil), rec.Addrs...)
		out = append(out, &Record{
			PeerID: rec.PeerID,
			Addrs:  addrs,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PeerID < out[j].PeerID
	})
	return out
}

func (c *Cache) load() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var records []*Record
	if err := json.Unmarshal(data, &records); err != nil {
		return err
	}
	for _, rec := range records {
		if rec == nil || rec.PeerID == "" {
			continue
		}
		c.records[rec.PeerID] = &Record{
			PeerID: rec.PeerID,
			Addrs:  append([]string(nil), rec.Addrs...),
			SeenAt: rec.SeenAt,
		}
	}
	return nil
}

func (c *Cache) persistLocked() error {
	records := make([]*Record, 0, len(c.records))
	for _, rec := range c.records {
		copyAddrs := append([]string(nil), rec.Addrs...)
		sort.Strings(copyAddrs)
		records = append(records, &Record{
			PeerID: rec.PeerID,
			Addrs:  copyAddrs,
			SeenAt: rec.SeenAt,
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].PeerID < records[j].PeerID
	})

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0o644)
}

func (c *Cache) trimLocked(limit int) {
	if limit <= 0 || len(c.records) <= limit {
		return
	}
	records := make([]*Record, 0, len(c.records))
	for _, rec := range c.records {
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].SeenAt == records[j].SeenAt {
			return records[i].PeerID < records[j].PeerID
		}
		return records[i].SeenAt < records[j].SeenAt
	})
	for len(records) > limit {
		rec := records[0]
		delete(c.records, rec.PeerID)
		records = records[1:]
	}
}
