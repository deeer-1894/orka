package llm

import (
	"context"
	"errors"
	"sync/atomic"
)

// swappable.go — changing the endpoint without a restart.
//
// The settings panel exists so nobody has to edit a file and restart to point
// this at a different provider. That only works if the client underneath can be
// replaced while the process runs, and it cannot be replaced by mutating its
// fields: BaseURL and APIKey are read on every request, from every in-flight
// run, so writing to them is a data race with real consequences (a torn read
// sends a request to one provider with another's credentials).
//
// So the provider client sits behind an atomic pointer, and a settings change
// installs a whole new one. It goes at the BOTTOM of the decorator stack —
// retry, metering and the rate limiter wrap it, not the other way round — so
// swapping the endpoint does not reset the limiter's in-flight accounting or
// lose the run-cost totals.

// Swappable is a Client whose implementation can be replaced atomically.
type Swappable struct {
	held atomic.Pointer[holder]
}

// holder exists because atomic.Pointer needs a concrete type and Client is an
// interface.
type holder struct{ c Client }

// NewSwappable wraps c. A nil client is allowed: it fails calls with a clear
// error rather than panicking, which is the right behaviour before anyone has
// configured an endpoint.
func NewSwappable(c Client) *Swappable {
	s := &Swappable{}
	s.Set(c)
	return s
}

// Set installs a new client for every subsequent call. Calls already in flight
// finish against the old one, which is correct — they were authenticated to it.
func (s *Swappable) Set(c Client) {
	s.held.Store(&holder{c: c})
}

// Current returns the installed client.
func (s *Swappable) Current() Client {
	if h := s.held.Load(); h != nil {
		return h.c
	}
	return nil
}

// ErrNoEndpoint is returned when no provider client is configured yet.
var ErrNoEndpoint = errors.New("llm: no endpoint configured — set one in settings")

func (s *Swappable) Chat(ctx context.Context, req Request) (Response, error) {
	c := s.Current()
	if c == nil {
		return Response{}, ErrNoEndpoint
	}
	return c.Chat(ctx, req)
}

// ChatStream forwards to the installed client when it streams. Swappable always
// implements StreamingClient because callers type-assert the OUTERMOST client:
// if this did not, wrapping a streaming provider would silently downgrade every
// run to non-streaming.
func (s *Swappable) ChatStream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	c := s.Current()
	if c == nil {
		return Response{}, ErrNoEndpoint
	}
	if sc, ok := c.(StreamingClient); ok {
		return sc.ChatStream(ctx, req, onDelta)
	}
	return c.Chat(ctx, req)
}
