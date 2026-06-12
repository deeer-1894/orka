package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// a Client whose Chat returns scripted (resp,err) pairs in order, counting calls.
type scriptedClient struct {
	calls int
	errs  []error // errs[i] returned on call i; nil = success
}

func (s *scriptedClient) Chat(_ context.Context, _ Request) (Response, error) {
	i := s.calls
	s.calls++
	if i < len(s.errs) && s.errs[i] != nil {
		return Response{}, s.errs[i]
	}
	return Response{Content: "ok"}, nil
}

func fastRetry(c Client, attempts int) *Retry {
	return NewRetry(c, RetryConfig{MaxAttempts: attempts, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond})
}

func TestRetrySucceedsAfterTransient(t *testing.T) {
	c := &scriptedClient{errs: []error{&APIError{Status: 503}, &APIError{Status: 429}, nil}}
	resp, err := fastRetry(c, 3).Chat(context.Background(), Request{})
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if resp.Content != "ok" || c.calls != 3 {
		t.Fatalf("calls=%d resp=%q", c.calls, resp.Content)
	}
}

func TestRetryGivesUpAfterMaxAttempts(t *testing.T) {
	c := &scriptedClient{errs: []error{&APIError{Status: 500}, &APIError{Status: 500}, &APIError{Status: 500}}}
	_, err := fastRetry(c, 3).Chat(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected final error")
	}
	if c.calls != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", c.calls)
	}
}

func TestRetryDoesNotRetryClientError(t *testing.T) {
	// 400/401/422 etc. won't improve on retry — fail fast.
	for _, status := range []int{400, 401, 403, 422} {
		c := &scriptedClient{errs: []error{&APIError{Status: status}}}
		_, err := fastRetry(c, 5).Chat(context.Background(), Request{})
		if err == nil {
			t.Fatalf("status %d: expected error", status)
		}
		if c.calls != 1 {
			t.Fatalf("status %d: expected fail-fast (1 call), got %d", status, c.calls)
		}
	}
}

func TestRetryStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &scriptedClient{errs: []error{&APIError{Status: 503}, nil}}
	_, err := fastRetry(c, 3).Chat(ctx, Request{})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	// First call happens, returns 503; retryable() sees ctx.Err() != nil → no retry.
	if c.calls != 1 {
		t.Fatalf("expected 1 call on cancelled ctx, got %d", c.calls)
	}
}

func TestRetryNetworkErrorIsTransient(t *testing.T) {
	c := &scriptedClient{errs: []error{errors.New("llm http: connection reset"), nil}}
	_, err := fastRetry(c, 3).Chat(context.Background(), Request{})
	if err != nil {
		t.Fatalf("network error should be retried, got %v", err)
	}
	if c.calls != 2 {
		t.Fatalf("expected 2 calls, got %d", c.calls)
	}
}
