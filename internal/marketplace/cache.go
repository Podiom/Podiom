package marketplace

import (
	"sync"
	"time"
)

// ttlCache is a tiny in-memory cache with per-entry expiry, mirroring the simple
// map+mutex style used elsewhere in the codebase. It backs SRC-4 (15 min search /
// 24 h detail). It uses a monotonic wall clock; entries are checked lazily on get.
type ttlCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}

func newTTLCache() *ttlCache {
	return &ttlCache{entries: map[string]cacheEntry{}}
}

func (c *ttlCache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return e.value, true
}

func (c *ttlCache) set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{value: value, expires: time.Now().Add(ttl)}
}
