package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

type followupKey struct{ owner, conversation, run string }
type followupEntry struct {
	profile     string
	done        chan struct{}
	suggestions []string
	err         error
	expires     time.Time
}

// A bounded process-local cache joins concurrent tabs and retains even an empty
// paid result. Credentials and source prompts are never retained here. Durable
// attempt attribution belongs to the budget ledger, not a second usage store.
type followupCache struct {
	mu      sync.Mutex
	entries map[followupKey]*followupEntry
}

var errFollowupCapacity = errors.New("followup generation capacity reached")

func (c *followupCache) get(ctx context.Context, key followupKey, profile string, generate func(context.Context) ([]string, error)) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[followupKey]*followupEntry{}
	}
	now := time.Now()
	for k, e := range c.entries {
		select {
		case <-e.done:
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		default:
		}
	}
	e := c.entries[key]
	if e != nil && e.profile != profile {
		c.mu.Unlock()
		return []string{}, nil
	}
	if e == nil {
		if len(c.entries) >= 256 {
			var oldestKey followupKey
			var oldest *followupEntry
			for k, v := range c.entries {
				select {
				case <-v.done:
					if oldest == nil || v.expires.Before(oldest.expires) {
						oldestKey, oldest = k, v
					}
				default:
				}
			}
			if oldest == nil {
				c.mu.Unlock()
				return nil, errFollowupCapacity
			}
			delete(c.entries, oldestKey)
		}
		e = &followupEntry{profile: profile, done: make(chan struct{})}
		c.entries[key] = e
		// A tab disconnect must not cancel another tab's already admitted request.
		// The shared generation has its own finite lifetime and one budget scope.
		generationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		go func() {
			defer cancel()
			result, err := generate(generationCtx)
			c.mu.Lock()
			e.suggestions = append([]string{}, result...)
			e.err = err
			e.expires = time.Now().Add(24 * time.Hour)
			close(e.done)
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return append([]string{}, e.suggestions...), e.err
	}
}
