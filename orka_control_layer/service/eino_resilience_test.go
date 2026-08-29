package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// flakyStreamClient fails mid-stream (after emitting a delta) on the first N
// calls, then succeeds. This reproduces the exact production failure that killed
// the factor pipeline: `stream read: unexpected EOF` AFTER tokens had started,
// which the transport-level retry in llm/retry.go refuses to replay.
type flakyStreamClient struct {
	mu        sync.Mutex
	failFirst int
	calls     int
	reply     string
}

func (f *flakyStreamClient) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	return f.ChatStream(ctx, req, func(string) {})
}

func (f *flakyStreamClient) ChatStream(_ context.Context, _ llm.Request, onDelta func(string)) (llm.Response, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()

	if n <= f.failFirst {
		onDelta("partial ") // a delta IS emitted → transport retry gives up here
		return llm.Response{}, errors.New("stream read: " + io.ErrUnexpectedEOF.Error())
	}
	onDelta(f.reply)
	return llm.Response{Content: f.reply, FinishReason: "stop"}, nil
}

func (f *flakyStreamClient) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestMidStreamFailureSurvivesViaADKRetry is the P0 acceptance test: a run whose
// model call dies mid-stream must still complete, because ADK consumes the whole
// stream before deciding and can retry a call the transport layer cannot replay.
func TestMidStreamFailureSurvivesViaADKRetry(t *testing.T) {
	ctx := context.Background()
	flaky := &flakyStreamClient{failFirst: 1, reply: "recovered answer"}

	ag, err := BuildEinoAgent(ctx, flaky, "m", "You are a helpful assistant.", nil, 4, nil)
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	out, err := RunEinoOnce(ctx, ag, "hi")
	if err != nil {
		t.Fatalf("run died on a mid-stream failure that should have been retried: %v", err)
	}
	if !strings.Contains(out, "recovered answer") {
		t.Fatalf("expected the retried attempt's answer, got %q", out)
	}
	if flaky.Calls() < 2 {
		t.Fatalf("expected a retry (>=2 model calls), got %d", flaky.Calls())
	}
}

// TestNonTransientIsNotRetried guards the other direction: a 4xx (bad key,
// moderation) must fail fast instead of burning retries.
func TestNonTransientIsNotRetried(t *testing.T) {
	ctx := context.Background()
	authFail := &statusErrClient{status: 401}

	ag, err := BuildEinoAgent(ctx, authFail, "m", "sys", nil, 4, nil)
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	if _, err = RunEinoOnce(ctx, ag, "hi"); err == nil {
		t.Fatal("expected the run to fail on a 401")
	}
	if got := authFail.Calls(); got != 1 {
		t.Fatalf("401 should not be retried: got %d model calls, want 1", got)
	}
}

type statusErrClient struct {
	mu     sync.Mutex
	status int
	calls  int
}

func (c *statusErrClient) Chat(_ context.Context, _ llm.Request) (llm.Response, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return llm.Response{}, &llm.APIError{Status: c.status}
}

func (c *statusErrClient) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// TestStreamResetEmittedOnRetry verifies the UI is told to discard the partial
// text from a failed attempt, so it isn't concatenated with the retry's output.
func TestStreamResetEmittedOnRetry(t *testing.T) {
	var mu sync.Mutex
	resets := 0
	emitCtx := agent.WithEmit(context.Background(), func(m messages.Message) {
		if m.Type == "stream" && m.Action == "reset" {
			mu.Lock()
			resets++
			mu.Unlock()
		}
	})

	flaky := &flakyStreamClient{failFirst: 1, reply: "ok"}
	ag, err := BuildEinoAgent(emitCtx, flaky, "m", "sys", nil, 4, nil)
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	if _, err := RunEinoOnce(emitCtx, ag, "hi"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if resets == 0 {
		t.Fatal("expected a stream-reset event when the model call was retried")
	}
}
