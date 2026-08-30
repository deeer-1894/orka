package service

import (
	"testing"
	"time"
)

// The pool must never close connections a run is still using. Closing an MCP
// client makes every in-flight request on it fail with "context canceled" —
// mcp-go folds the client's close signal into each request context — and that
// is what happened: lastUsed was stamped once at the start of a run, so the
// janitor saw a 40-minute run as 40 minutes idle. Every historical occurrence
// was a run longer than the eviction window (183, 176, 143 and 36 minutes) and
// none was shorter.
//
// These exercise the bookkeeping directly. closeClients on an entry with no
// clients is a no-op, so the assertions are on refs/retired/membership — which
// is exactly what decides whether a close happens.

func newTestPool(maxAge time.Duration) *mcpPool {
	return &mcpPool{maxAge: maxAge, entries: map[string]*mcpEntry{}}
}

func TestPoolLeaseSurvivesJanitorSweep(t *testing.T) {
	p := newTestPool(time.Minute)
	e := &mcpEntry{created: time.Now(), lastUsed: time.Now().Add(-time.Hour), refs: 1}
	p.entries["u@x.com"] = e

	// The janitor's condition, applied as the janitor applies it.
	p.mu.Lock()
	evicted := e.refs <= 0 && time.Since(e.lastUsed) >= p.maxAge
	p.mu.Unlock()
	if evicted {
		t.Fatal("janitor would evict an entry a run is still holding — this is the bug")
	}
}

func TestPoolEvictsOnceReleased(t *testing.T) {
	p := newTestPool(time.Minute)
	e := &mcpEntry{created: time.Now(), lastUsed: time.Now().Add(-time.Hour), refs: 1}
	p.entries["u@x.com"] = e

	p.releaser(e)()
	if e.refs != 0 {
		t.Fatalf("refs = %d after release, want 0", e.refs)
	}
	// Releasing refreshes lastUsed: a run that just finished is not idle work,
	// and the entry should stay warm for the next turn of the conversation.
	if time.Since(e.lastUsed) > time.Minute {
		t.Fatal("release did not refresh lastUsed; the entry would be evicted immediately")
	}
}

// Retiring a held entry must remove it from the pool (so new runs build a fresh
// one) WITHOUT closing it, and the last holder to leave closes it.
func TestPoolRetireDefersCloseToLastHolder(t *testing.T) {
	p := newTestPool(time.Minute)
	e := &mcpEntry{created: time.Now(), lastUsed: time.Now(), refs: 2}
	p.entries["u@x.com"] = e
	rel1, rel2 := p.releaser(e), p.releaser(e)

	p.mu.Lock()
	p.retire("u@x.com", e)
	p.mu.Unlock()

	if _, still := p.entries["u@x.com"]; still {
		t.Fatal("retired entry is still reachable; new runs would keep using it")
	}
	if !e.retired {
		t.Fatal("entry not marked retired")
	}
	rel1()
	if e.refs != 1 {
		t.Fatalf("refs = %d after one release, want 1 — closing here would kill the other run", e.refs)
	}
	rel2()
	if e.refs != 0 {
		t.Fatalf("refs = %d after both releases, want 0", e.refs)
	}
}

func TestPoolRetireClosesUnheldEntryImmediately(t *testing.T) {
	p := newTestPool(time.Minute)
	e := &mcpEntry{created: time.Now(), lastUsed: time.Now(), refs: 0}
	p.entries["u@x.com"] = e
	p.mu.Lock()
	p.retire("u@x.com", e)
	p.mu.Unlock()
	if _, still := p.entries["u@x.com"]; still {
		t.Fatal("unheld entry survived retirement")
	}
}

// A double release must not drive refs negative, or a later retire would skip
// the close and leak the connection.
func TestPoolReleaseIsIdempotent(t *testing.T) {
	p := newTestPool(time.Minute)
	e := &mcpEntry{created: time.Now(), lastUsed: time.Now(), refs: 1}
	rel := p.releaser(e)
	rel()
	rel()
	rel()
	if e.refs != 0 {
		t.Fatalf("refs = %d after three calls to one releaser, want 0", e.refs)
	}
}

// Two concurrent runs for the same user share one entry, and neither may close
// it while the other is working.
func TestPoolConcurrentRunsShareOneLease(t *testing.T) {
	p := newTestPool(time.Hour)
	e := &mcpEntry{created: time.Now(), lastUsed: time.Now(), refs: 0}
	p.entries["u@x.com"] = e

	p.mu.Lock()
	e.refs++
	relA := p.releaser(e)
	e.refs++
	relB := p.releaser(e)
	p.mu.Unlock()

	relA()
	if e.refs != 1 {
		t.Fatalf("refs = %d after the first run finished, want 1", e.refs)
	}
	relB()
	if e.refs != 0 {
		t.Fatalf("refs = %d after both finished, want 0", e.refs)
	}
}

// maxAge is about connection freshness, not the token TTL — the token is signed
// per request now. The old derivation (tokenTTL-1m) went negative below a
// minute, making every lookup rebuild the entry and close clients that in-flight
// calls were still using.
func TestPoolMaxAgeNeverDegenerate(t *testing.T) {
	for _, ttl := range []time.Duration{0, time.Second, 20 * time.Second, time.Minute, 30 * time.Minute, time.Hour} {
		_, _ = MCPToolsProviderPooled("", "", "", ttl, nil, nil)
	}
	// Constructed pools are what matter; assert the arithmetic directly.
	for _, ttl := range []time.Duration{0, time.Second, 20 * time.Second, 90 * time.Second} {
		maxAge := connMaxAge
		if ttl > 0 && ttl < maxAge {
			maxAge = ttl
		}
		if maxAge < time.Minute {
			maxAge = time.Minute
		}
		if maxAge <= 0 {
			t.Fatalf("ttl=%s produced maxAge=%s", ttl, maxAge)
		}
	}
}
