package geoip

import (
	"sync"
	"time"
)

type cacheEntry struct {
	country string
	until   time.Time
}

// CachedResolver wraps a Resolver with an in-memory cache (IP -> country) and TTL.
type CachedResolver struct {
	inner Resolver
	ttl   time.Duration
	mu    sync.RWMutex
	cache map[string]cacheEntry
}

// NewCachedResolver returns a resolver that caches results for ttl.
func NewCachedResolver(inner Resolver, ttl time.Duration) *CachedResolver {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &CachedResolver{
		inner: inner,
		ttl:   ttl,
		cache: make(map[string]cacheEntry),
	}
}

// Country returns the cached country if valid, otherwise looks up and caches.
func (c *CachedResolver) Country(ip string) (string, error) {
	if ip == "" {
		return "", nil
	}
	c.mu.RLock()
	if e, ok := c.cache[ip]; ok && time.Now().Before(e.until) {
		c.mu.RUnlock()
		return e.country, nil
	}
	c.mu.RUnlock()

	country, err := c.inner.Country(ip)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.cache[ip] = cacheEntry{country: country, until: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return country, nil
}
