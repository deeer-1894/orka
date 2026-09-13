package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_control_layer/llm"
)

// thinker streams reasoning chunks until cancelled, optionally producing content
// after `actAfter` chunks. It records whether — and why — it was cancelled,
// because a stop that does not cancel the provider keeps generating and billing.
type thinker struct {
	chunk    string
	actAfter int // -1 = never acts
	cause    chan error
}

func (t *thinker) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant, Content: "ok"}, nil
}

func (t *thinker) Stream(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()
		for i := 0; i < 10_000; i++ {
			select {
			case <-ctx.Done():
				t.cause <- context.Cause(ctx)
				return
			default:
			}
			msg := &schema.Message{Role: schema.Assistant, ReasoningContent: t.chunk}
			if t.actAfter >= 0 && i >= t.actAfter {
				msg = &schema.Message{Role: schema.Assistant, Content: "writing the file", ReasoningContent: t.chunk}
			}
			if closed := sw.Send(msg, nil); closed {
				t.cause <- context.Cause(ctx)
				return
			}
			if t.actAfter >= 0 && i >= t.actAfter+50 {
				t.cause <- nil
				return
			}
		}
		t.cause <- nil
	}()
	return sr, nil
}

type turnRecorder struct{ turns []llm.Turn }

func (r *turnRecorder) ObserveTurn(t llm.Turn) { r.turns = append(r.turns, t) }

func drain(t *testing.T, sr *schema.StreamReader[*schema.Message]) (finish string, reasoning int) {
	t.Helper()
	for {
		m, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		reasoning += len([]rune(m.ReasoningContent))
		if m.ResponseMeta != nil && m.ResponseMeta.FinishReason != "" {
			finish = m.ResponseMeta.FinishReason
		}
	}
}

func budgeted(t *testing.T, inner model.BaseChatModel, runes int) model.BaseChatModel {
	t.Helper()
	wrapped, err := newReasoningBudget(runes, einoOrchestratorName).WrapModel(context.Background(), inner, nil)
	if err != nil {
		t.Fatal(err)
	}
	return wrapped
}

func waitCause(t *testing.T, ch chan error) error {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("the provider call was never released")
		return nil
	}
}

// The measured failure: reasoning that never turns into an action. The turn must
// end, and the PROVIDER must be cancelled — stopping only our reader would leave
// it generating and billing for minutes.
func TestAModelThatOnlyThinksIsStoppedAtTheBudget(t *testing.T) {
	th := &thinker{chunk: "let me consider the viewBox again ", actAfter: -1, cause: make(chan error, 1)}
	rec := &turnRecorder{}
	ctx := llm.WithTurnSink(context.Background(), rec)

	sr, err := budgeted(t, th, 1000).Stream(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	finish, spent := drain(t, sr)
	if finish != llm.FinishReasoningBudget {
		t.Fatalf("finish = %q, want the budget stop", finish)
	}
	if spent < 1000 || spent > 1000+len(th.chunk) {
		t.Fatalf("stopped after %d runes of reasoning; budget was 1000", spent)
	}
	if cause := waitCause(t, th.cause); !errors.Is(cause, llm.ErrReasoningBudget) {
		t.Fatalf("provider call ended with cause %v — it must be cancelled, and say why", cause)
	}
	if len(rec.turns) != 1 || !rec.turns[0].StoppedThinking() {
		t.Fatalf("turn not recorded as a budget stop: %+v", rec.turns)
	}
	if !strings.Contains(rec.turns[0].Reasoning, "viewBox") {
		t.Fatal("the plan was not kept — the next turn would have to make it again")
	}
}

// A model that has started acting is doing its job, however much it goes on to
// think. The budget is on thinking INSTEAD of acting, not thinking at all.
func TestAModelThatActsIsNeverStopped(t *testing.T) {
	// 10 chunks × 4 runes = 40 runes of thinking before it acts (under the 100
	// budget), then 50 more chunks = 200 runes of thinking AFTER it has acted
	// (well over it). Only the first part may count.
	th := &thinker{chunk: "hmm ", actAfter: 10, cause: make(chan error, 1)}
	sr, err := budgeted(t, th, 100).Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if finish, _ := drain(t, sr); finish == llm.FinishReasoningBudget {
		t.Fatal("stopped a turn that had already produced content")
	}
	if cause := waitCause(t, th.cause); cause != nil {
		t.Fatalf("a turn that acted was cancelled: %v", cause)
	}
}

// Deep thinking ON means the user asked for the long think.
func TestDeepThinkingOnDisablesTheBudget(t *testing.T) {
	th := &thinker{chunk: "carefully ", actAfter: 200, cause: make(chan error, 1)}
	ctx := llm.WithThinking(context.Background(), llm.ThinkingOn)
	sr, err := budgeted(t, th, 50).Stream(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if finish, spent := drain(t, sr); finish == llm.FinishReasoningBudget || spent < 1000 {
		t.Fatalf("budget applied despite deep thinking on (finish=%q, spent=%d)", finish, spent)
	}
}

func TestAZeroBudgetInstallsNothing(t *testing.T) {
	if newReasoningBudget(0, "x") != nil {
		t.Fatal("a disabled budget returned a middleware")
	}
}

// The continuation must hand the plan back with the instruction to act on it.
func TestContinuationHandsThePlanBack(t *testing.T) {
	tr := newTurnTracker()
	ctx := withTurnTracker(context.Background(), tr)
	tr.ObserveTurn(llm.Turn{Agent: einoOrchestratorName, FinishReason: llm.FinishReasoningBudget, Reasoning: "use SMIL animateTransform for the wheels"})

	nudge := continuationNudge(ctx)
	if !strings.HasPrefix(nudge, reasoningBudgetNudgePrefix) {
		t.Fatalf("wrong nudge: %.60q", nudge)
	}
	if !strings.Contains(nudge, "animateTransform") {
		t.Fatal("the plan was not included")
	}
}
