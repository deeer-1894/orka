package service

import (
	"context"
	"errors"
	"testing"
)

func TestClassifyToolError(t *testing.T) {
	cases := []struct {
		msg  string
		want toolFailureKind
	}{
		// The single biggest source of tool failures measured here.
		{`mcp call "shell": transport error: failed to send request`, failTransient},
		{"connection refused", failTransient},
		{"stream read: unexpected EOF", failTransient},
		{"context deadline exceeded", failTransient},
		// Arrives wrapped as a transport error, but retrying it under the same
		// dead context can only fail again. eino cancels the branch whenever it
		// retries a dropped model stream — the top killer of long runs here.
		{`mcp call "file_write": transport error: failed to send request: context canceled`, failCancelled},
		{"upstream returned 503", failTransient},
		// An expired token is not a permission decision, and must not be
		// classified as one — that confusion is what this work started from.
		{"auth failed (retryable — the caller should refresh its context token): token expired", failAuth},
		{`tool "file_write" error: permission denied: missing scope file:write`, failDenied},
		{"no such file or directory", failOther},
		{"invalid arguments", failOther},
	}
	for _, c := range cases {
		if got := classifyToolError(c.msg); got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestRetryTransientRetriesAndSucceeds(t *testing.T) {
	calls := 0
	out, err, _ := retryTransient(context.Background(), func() (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("transport error: failed to send request")
		}
		return "ok", nil
	})
	if err != nil || out != "ok" {
		t.Fatalf("got %q/%v, want ok/nil", out, err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (initial + 2 retries)", calls)
	}
}

func TestRetryTransientGivesUpAfterBudget(t *testing.T) {
	calls := 0
	_, err, _ := retryTransient(context.Background(), func() (string, error) {
		calls++
		return "", errors.New("connection refused")
	})
	if err == nil {
		t.Fatal("expected the error to survive an exhausted retry budget")
	}
	if calls != toolRetries+1 {
		t.Fatalf("calls = %d, want %d", calls, toolRetries+1)
	}
}

// Retrying a permission denial would burn the budget on something that can
// never succeed, and retrying a bad-argument error is equally pointless.
func TestRetryTransientDoesNotRetryNonTransient(t *testing.T) {
	for _, msg := range []string{"permission denied: missing scope file:write", "invalid arguments"} {
		calls := 0
		_, _, _ = retryTransient(context.Background(), func() (string, error) {
			calls++
			return "", errors.New(msg)
		})
		if calls != 1 {
			t.Errorf("%q: calls = %d, want 1", msg, calls)
		}
	}
}

func TestRetryTransientStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, _, _ = retryTransient(ctx, func() (string, error) {
		calls++
		return "", errors.New("transport error")
	})
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 — a cancelled run must not keep retrying", calls)
	}
}

func TestRetryTransientLeavesSuccessAlone(t *testing.T) {
	calls := 0
	out, err, _ := retryTransient(context.Background(), func() (string, error) {
		calls++
		return "tool reported a failure in its output", nil
	})
	if calls != 1 || err != nil || out == "" {
		t.Fatalf("calls=%d err=%v out=%q — a successful call must never be repeated", calls, err, out)
	}
}

// The advice has to match the failure. Telling the model to "try a different
// approach" after a dropped socket sends it away from the thing that would have
// worked; telling it to keep trying after a denial wastes the run.
func TestToolErrorMessageAdviceMatchesCause(t *testing.T) {
	transient := toolErrorMessage("shell", errors.New("transport error: failed to send request"), toolRetries)
	if !contains(transient, "retried 2x") {
		t.Errorf("transient message does not report the retries: %s", transient)
	}
	// A transient failure that was NOT retried (the run was going away) must not
	// claim it was — the whole point of this file is that the text be truthful.
	notRetried := toolErrorMessage("shell", errors.New("transport error"), 0)
	if !contains(notRetried, "not retried") {
		t.Errorf("claims retries that never happened: %s", notRetried)
	}
	// A cancelled call is upstream's doing, not the tool's.
	cancelled := toolErrorMessage("file_write", errors.New(`mcp call "file_write": transport error: failed to send request: context canceled`), 0)
	if !contains(cancelled, "cancelled upstream") {
		t.Errorf("a cancelled call is reported as a tool failure: %s", cancelled)
	}
	denied := toolErrorMessage("file_write", errors.New("permission denied: missing scope file:write"), 0)
	if !contains(denied, "do not retry") {
		t.Errorf("denial message does not tell the model to stop: %s", denied)
	}
	auth := toolErrorMessage("file_read", errors.New("auth failed: token expired"), 0)
	if !contains(auth, "not a permission problem") {
		t.Errorf("auth message does not distinguish itself from a denial: %s", auth)
	}
	// Every message must still name the tool and carry the underlying cause,
	// which the old single wrapper buried behind its own boilerplate.
	for _, m := range []string{transient, denied, auth} {
		if !contains(m, ":") {
			t.Errorf("message drops the underlying cause: %s", m)
		}
	}
	if !contains(transient, "shell") || !contains(denied, "file_write") {
		t.Error("messages do not name the failing tool")
	}
}
