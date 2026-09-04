package llm

import (
	"errors"
	"testing"
	"time"
)

// A rate limit is not a transport blip. It is the provider saying the CURRENT
// rate is too high, so retrying in a fraction of a second re-asserts exactly the
// rate it objected to. Ark's burst protection says as much in the body — "Please
// slow down traffic growth and increase requests gradually before retrying" —
// and a run died against it after retries at 377ms and 957ms, 738k tokens in.
func TestRateLimitBacksOffFarLongerThanATransportBlip(t *testing.T) {
	r := NewRetry(nil, RetryConfig{})
	rateLimit := &APIError{Status: 429, Body: `{"error":{"code":"RequestBurstTooFast"}}`}
	blip := &APIError{Status: 503}

	for attempt := 1; attempt <= 3; attempt++ {
		limited := r.backoffFor(attempt, rateLimit)
		transient := r.backoffFor(attempt, blip)
		if limited <= transient {
			t.Errorf("attempt %d: 429 waited %v, 503 waited %v — a rate limit must back off further",
				attempt, limited, transient)
		}
		if limited < 4*time.Second {
			t.Errorf("attempt %d: 429 waited %v, which re-asserts the rejected rate", attempt, limited)
		}
		if limited > 60*time.Second {
			t.Errorf("attempt %d: 429 waited %v, long enough to strand the run", attempt, limited)
		}
	}
}

// Non-rate-limit failures keep the fast path: a network blip should not cost
// six seconds.
func TestTransientErrorsKeepTheShortBackoff(t *testing.T) {
	r := NewRetry(nil, RetryConfig{})
	for _, err := range []error{
		&APIError{Status: 500},
		errors.New("llm http: connection reset"),
	} {
		if d := r.backoffFor(1, err); d > 2*time.Second {
			t.Errorf("%v backed off %v; only a rate limit should wait that long", err, d)
		}
	}
}

// Concurrent callers must not resynchronise on the same instant — against a
// burst limit that turns one 429 into a wave of them.
func TestBackoffIsJittered(t *testing.T) {
	r := NewRetry(nil, RetryConfig{})
	seen := map[time.Duration]bool{}
	for i := 0; i < 32; i++ {
		seen[r.backoffFor(2, &APIError{Status: 429})] = true
	}
	if len(seen) < 8 {
		t.Errorf("only %d distinct delays across 32 draws; retries would synchronise", len(seen))
	}
}
