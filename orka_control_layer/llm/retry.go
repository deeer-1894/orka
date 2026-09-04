package llm

import (
	"context"
	"errors"
	"math/rand"
	"time"
)

// Retry decorates a Client with bounded exponential backoff on transient
// failures (HTTP 429 / 5xx and network errors), mirroring Eino's
// ModelRetryConfig. A single transient blip from the provider would otherwise
// fail an entire agentic run (and, under multi-agent, a sub-agent delegation);
// this makes the LLM call resilient without touching call sites.
//
// It also implements StreamingClient when the wrapped client does, but only
// retries a stream that fails BEFORE its first token — a partially-streamed
// response can't be safely replayed to onDelta (it would duplicate tokens), so
// once tokens have flowed the error is surfaced as-is.
type Retry struct {
	Client
	cfg RetryConfig
}

// RetryConfig tunes the backoff. Zero values fall back to sane defaults.
type RetryConfig struct {
	MaxAttempts int           // total attempts including the first (default 3)
	BaseDelay   time.Duration // backoff before the 2nd attempt (default 400ms)
	MaxDelay    time.Duration // per-attempt cap (default 8s)
	// OnRetry, if set, is called before each retry sleep (for logging/metrics).
	OnRetry func(attempt int, delay time.Duration, err error)
}

// NewRetry wraps c so its Chat/ChatStream retry transient errors.
func NewRetry(c Client, cfg RetryConfig) *Retry {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 400 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 8 * time.Second
	}
	return &Retry{Client: c, cfg: cfg}
}

// Chat retries the wrapped Chat on transient errors.
func (r *Retry) Chat(ctx context.Context, req Request) (Response, error) {
	var lastErr error
	for attempt := 1; attempt <= r.cfg.MaxAttempts; attempt++ {
		resp, err := r.Client.Chat(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt == r.cfg.MaxAttempts || !retryable(ctx, err) {
			return Response{}, err
		}
		if err := r.wait(ctx, attempt, err); err != nil {
			return Response{}, err
		}
	}
	return Response{}, lastErr
}

// ChatStream retries only before the first streamed token.
func (r *Retry) ChatStream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	sc, ok := r.Client.(StreamingClient)
	if !ok {
		return r.Chat(ctx, req) // already retried
	}
	var lastErr error
	for attempt := 1; attempt <= r.cfg.MaxAttempts; attempt++ {
		started := false
		resp, err := sc.ChatStream(ctx, req, func(d string) {
			started = true
			onDelta(d)
		})
		if err == nil {
			return resp, nil
		}
		lastErr = err
		// A partial stream can't be replayed; surface the error as-is.
		if started || attempt == r.cfg.MaxAttempts || !retryable(ctx, err) {
			return resp, err
		}
		if err := r.wait(ctx, attempt, err); err != nil {
			return Response{}, err
		}
	}
	return Response{}, lastErr
}

// wait sleeps for the backoff of the given attempt, honoring ctx cancellation.
func (r *Retry) wait(ctx context.Context, attempt int, cause error) error {
	delay := r.backoffFor(attempt, cause)
	if r.cfg.OnRetry != nil {
		r.cfg.OnRetry(attempt, delay, cause)
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// rateLimitBaseDelay is the starting backoff for a 429, as opposed to the
// sub-second one that suits a transport blip.
//
// A rate limit is not a blip: it is the provider stating that the CURRENT rate
// is too high, so retrying in 377ms re-asserts exactly the rate it objected to.
// Ark's burst protection says so in the error — "System protection triggered by
// request burst. Please slow down traffic growth and increase requests gradually
// before retrying" — and a run here died against it after retries at 377ms and
// 957ms, having spent 738k tokens first.
const rateLimitBaseDelay = 6 * time.Second

// rateLimitMaxDelay lets the third attempt land far enough out to matter. The
// alternative to waiting is losing the run.
const rateLimitMaxDelay = 45 * time.Second

func (r *Retry) backoffFor(attempt int, err error) time.Duration {
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 429 {
		d := rateLimitBaseDelay << (attempt - 1)
		if d > rateLimitMaxDelay || d <= 0 {
			d = rateLimitMaxDelay
		}
		return jittered(d)
	}
	return r.backoff(attempt)
}

// backoff returns base*2^(attempt-1), capped at MaxDelay, with ±20% jitter to
// avoid synchronized retries across concurrent sub-agents.
func (r *Retry) backoff(attempt int) time.Duration {
	d := r.cfg.BaseDelay << (attempt - 1)
	if d > r.cfg.MaxDelay || d <= 0 {
		d = r.cfg.MaxDelay
	}
	return jittered(d)
}

// jittered spreads retries so concurrent callers do not resynchronise on the
// same instant — which, against a burst limit, is what turns one 429 into many.
func jittered(d time.Duration) time.Duration {
	jitter := 1 + (rand.Float64()*0.4 - 0.2) // [0.8, 1.2)
	return time.Duration(float64(d) * jitter)
}

// IsTransient reports whether err is a transient failure worth retrying. It is
// the single classifier shared by this transport-level wrapper and the ADK-level
// ModelRetryConfig, so both layers agree on what "worth retrying" means.
func IsTransient(ctx context.Context, err error) bool { return retryable(ctx, err) }

// retryable reports whether err is a transient failure worth retrying. A
// cancelled/expired context is never retried (the run is going away).
func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var ae *APIError
	if errors.As(err, &ae) {
		switch ae.Status {
		case 408, 409, 425, 429, 500, 502, 503, 504, 529:
			return true
		default:
			return false // 4xx (bad request, auth, moderation) won't improve on retry
		}
	}
	// Non-API errors are transport/network failures (e.g. "llm http: ...") which
	// are typically transient.
	return true
}
