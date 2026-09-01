package service

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// failingMiddleware stands in for the summarizer on a bad day.
type failingMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	err     error
	rewrote bool
}

func (f *failingMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	f.rewrote = true
	state.Messages = append(state.Messages, schema.UserMessage("summarised"))
	return ctx, state, nil
}

// Context management exists to keep the context small. Failing to do so should
// leave a bigger context, not a dead run — eino treats a middleware error as a
// NodeRunError and aborts, which killed 11 runs here, one of them after it had
// already finished the work.
func TestBestEffortSwallowsMiddlewareFailure(t *testing.T) {
	mw := bestEffort(&failingMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		err:                          errors.New("summary content is empty"),
	})
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("hi")}}
	ctx, out, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatalf("a failed summary aborted the run: %v", err)
	}
	if ctx == nil || out == nil {
		t.Fatal("returned a nil context or state, which would panic downstream")
	}
	if len(out.Messages) != 1 {
		t.Fatalf("state was mutated by a failed middleware: %d messages", len(out.Messages))
	}
}

// Swallowing errors must not swallow the WORK: a middleware that succeeds still
// has its rewrite applied, or context management silently stops happening.
func TestBestEffortPassesSuccessThrough(t *testing.T) {
	inner := &failingMiddleware{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}}
	mw := bestEffort(inner)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("hi")}}
	_, out, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !inner.rewrote {
		t.Fatal("the wrapped middleware never ran")
	}
	if len(out.Messages) != 2 {
		t.Fatalf("the successful rewrite was discarded: %d messages", len(out.Messages))
	}
}
