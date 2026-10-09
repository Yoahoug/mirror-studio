package risk

import (
	"sync"
	"time"
)

const counterSweepThreshold = 4096

// Counters is the in-process stand-in for the reference's LocMemCache
// counters. Windows are fixed at the first increment and never extended, which
// is how Django's locmem backend treats cache.incr; a window of zero or less
// expires immediately, matching timeout=0.
type Counters struct {
	mu      sync.Mutex
	entries map[string]counterEntry
}

type counterEntry struct {
	value     int64
	expiresAt time.Time
}

// NewCounters creates an empty counter set.
func NewCounters() *Counters {
	return &Counters{entries: map[string]counterEntry{}}
}

// Increment bumps key and returns the value inside the current window.
func (c *Counters) Increment(key string, window time.Duration) int64 {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= counterSweepThreshold {
		for name, entry := range c.entries {
			if !now.Before(entry.expiresAt) {
				delete(c.entries, name)
			}
		}
	}
	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expiresAt) {
		c.entries[key] = counterEntry{value: 1, expiresAt: now.Add(window)}
		return 1
	}
	entry.value++
	c.entries[key] = entry
	return entry.value
}

func (c *Counters) peek(key string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !time.Now().Before(entry.expiresAt) {
		return 0
	}
	return entry.value
}

func (c *Counters) reset(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}
