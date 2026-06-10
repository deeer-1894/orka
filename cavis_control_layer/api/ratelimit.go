package api

import (
	"sync"
	"time"
)

// rateLimiter is a small in-memory fixed-window limiter keyed by an arbitrary
// string (e.g. "login:<ip>" or "login:<email>"). It is process-local — fine for
// a single-node OSS deployment; a multi-node setup would back this with Redis.
type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string]*rlWindow
	limit  int
	window time.Duration
}

type rlWindow struct {
	count int
	reset time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{hits: map[string]*rlWindow{}, limit: limit, window: window}
	go rl.janitor()
	return rl
}

// allow records one hit for key and reports whether it is within the limit.
func (rl *rateLimiter) allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	w := rl.hits[key]
	if w == nil || now.After(w.reset) {
		rl.hits[key] = &rlWindow{count: 1, reset: now.Add(rl.window)}
		return true
	}
	w.count++
	return w.count <= rl.limit
}

// janitor evicts expired windows so the map does not grow without bound.
func (rl *rateLimiter) janitor() {
	t := time.NewTicker(rl.window)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		rl.mu.Lock()
		for k, w := range rl.hits {
			if now.After(w.reset) {
				delete(rl.hits, k)
			}
		}
		rl.mu.Unlock()
	}
}
