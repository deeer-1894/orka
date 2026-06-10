package connectors

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/orka-oss/orka_core/state"
)

// UserInfo is the displayable owner profile attached to tasks.
type UserInfo struct {
	Email  string `json:"email"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

// UserDirectory resolves emails to profiles (e.g. a Lark contact service).
type UserDirectory interface {
	// Lookup resolves the given emails in one batch.
	Lookup(ctx context.Context, emails []string) (map[string]UserInfo, error)
}

// StubDirectory derives a display name from the email local part. It records how
// many emails it was asked to resolve (used to prove cache effectiveness).
type StubDirectory struct {
	Calls int // total emails resolved through the underlying source
}

func (d *StubDirectory) Lookup(_ context.Context, emails []string) (map[string]UserInfo, error) {
	out := make(map[string]UserInfo, len(emails))
	for _, e := range emails {
		d.Calls++
		name := e
		if i := strings.IndexByte(e, '@'); i > 0 {
			name = e[:i]
		}
		out[e] = UserInfo{Email: e, Name: name}
	}
	return out, nil
}

// CachedDirectory wraps a UserDirectory with a cache-first + batch-miss +
// backfill strategy, avoiding the N+1 fan-out on task list pages.
type CachedDirectory struct {
	inner UserDirectory
	store state.StateStore
	ttl   time.Duration
}

// NewCachedDirectory wraps inner with a cache backed by store.
func NewCachedDirectory(inner UserDirectory, store state.StateStore, ttl time.Duration) *CachedDirectory {
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &CachedDirectory{inner: inner, store: store, ttl: ttl}
}

func (d *CachedDirectory) key(email string) string { return "userinfo:" + email }

// Lookup serves hits from cache, batches the misses to the inner source in a
// single call, then backfills the cache.
func (d *CachedDirectory) Lookup(ctx context.Context, emails []string) (map[string]UserInfo, error) {
	out := make(map[string]UserInfo, len(emails))
	var misses []string
	seen := map[string]bool{}
	for _, e := range emails {
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		if b, err := d.store.Get(ctx, d.key(e)); err == nil {
			var ui UserInfo
			if json.Unmarshal(b, &ui) == nil {
				out[e] = ui
				continue
			}
		}
		misses = append(misses, e)
	}
	if len(misses) > 0 {
		fresh, err := d.inner.Lookup(ctx, misses)
		if err != nil {
			return out, err
		}
		for e, ui := range fresh {
			out[e] = ui
			if b, err := json.Marshal(ui); err == nil {
				_ = d.store.Set(ctx, d.key(e), b, d.ttl)
			}
		}
	}
	return out, nil
}
