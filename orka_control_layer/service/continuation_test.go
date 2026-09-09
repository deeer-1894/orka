package service

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/messages"

	"github.com/orka-oss/orka_control_layer/llm"
)

func trackerCtx(turns ...llm.Turn) (context.Context, *turnTracker) {
	tt := newTurnTracker()
	ctx := withTurnTracker(context.Background(), tt)
	for _, t := range turns {
		tt.ObserveTurn(t)
	}
	return ctx, tt
}

// The run this exists for: 693 seconds, ONE model call, zero tool calls, no
// file written, filed as done. The model spent the whole call composing an SVG
// inside its reasoning, hit the provider's ceiling, and returned mid-tag.
func TestATruncatedTurnWithNoToolCallDoesNotEndTheRun(t *testing.T) {
	ctx, _ := trackerCtx(llm.Turn{
		Agent: einoOrchestratorName, FinishReason: "length", ToolCalls: 0, ReasoningRunes: 82903,
	})
	nudge := continuationNudge(ctx)
	if nudge == "" {
		t.Fatal("a run that was cut off mid-thought was treated as finished")
	}
	// The instruction has to say what to do differently, or the next turn is the
	// same turn: more reasoning, truncated again, still no tool call.
	for _, want := range []string{"截断", "file_write"} {
		if !strings.Contains(nudge, want) {
			t.Errorf("the instruction omits %q: %s", want, nudge)
		}
	}
}

// A truncated turn that DID call tools is ordinary progress: the model was
// mid-work and the tool results come back regardless. Pushing it would inject a
// scolding into a run that is behaving.
func TestATruncatedTurnThatCalledToolsIsLeftAlone(t *testing.T) {
	ctx, _ := trackerCtx(llm.Turn{Agent: einoOrchestratorName, FinishReason: "length", ToolCalls: 2})
	if n := continuationNudge(ctx); n != "" {
		t.Fatalf("interrupted a working run: %s", n)
	}
}

func TestAFinishedTurnEndsTheRun(t *testing.T) {
	ctx, _ := trackerCtx(llm.Turn{Agent: einoOrchestratorName, FinishReason: "stop", ContentRunes: 400})
	if n := continuationNudge(ctx); n != "" {
		t.Fatalf("a completed run was pushed back to work: %s", n)
	}
}

// Only the orchestrator's turn ends the run. A delegate's truncated answer is
// the delegate's problem and reaches its caller as a short tool result; acting
// on it here would restart the orchestrator over someone else's turn.
func TestADelegatesTruncatedTurnIsNotTheRunsLastWord(t *testing.T) {
	ctx, _ := trackerCtx(
		llm.Turn{Agent: einoOrchestratorName, FinishReason: "stop", ContentRunes: 200},
		llm.Turn{Agent: "researcher", FinishReason: "length", ToolCalls: 0},
	)
	if n := continuationNudge(ctx); n != "" {
		t.Fatalf("a sub-agent's truncation ended up driving the orchestrator: %s", n)
	}
}

// The other half: four runs made 65-104 tool calls, never came near the budget,
// then stopped with 1-6 items of their own checklist undone.
func TestAnUnfinishedChecklistDoesNotEndTheRun(t *testing.T) {
	ctx, _ := trackerCtx(llm.Turn{Agent: einoOrchestratorName, FinishReason: "stop", ContentRunes: 300})
	plan := &planTracker{}
	ctx = withPlanTracker(ctx, plan)
	plan.record([]messages.PlanStep{
		{Title: "写 corpus.py", Status: "done"},
		{Title: "跑基准并记录数据", Status: "active"},
		{Title: "汇总成报告", Status: "pending"},
	})

	nudge := continuationNudge(ctx)
	if nudge == "" {
		t.Fatal("a run that abandoned its own plan was treated as finished")
	}
	for _, want := range []string{"跑基准并记录数据", "汇总成报告"} {
		if !strings.Contains(nudge, want) {
			t.Errorf("the instruction omits the unfinished step %q", want)
		}
	}
	if strings.Contains(nudge, "写 corpus.py") {
		t.Error("the instruction lists a step that was already done")
	}
}

// A run that spent its allowance has a real reason to stop. Pushing it back
// would spend budget it does not have, and the record already says so.
func TestAnExhaustedBudgetIsNotPushedBackToWork(t *testing.T) {
	ctx, _ := trackerCtx(llm.Turn{Agent: einoOrchestratorName, FinishReason: "length", ToolCalls: 0})
	b := newRunBudget(200, 100, 0)
	b.AddUsage(80, 40)                                              // past the token allowance
	b.observe([]*schema.Message{schema.AssistantMessage("x", nil)}) // the guard's own check trips it
	ctx = withBudget(ctx, b)
	if b.exhausted() == "" {
		t.Fatal("fixture did not exhaust the budget")
	}
	if n := continuationNudge(ctx); n != "" {
		t.Fatalf("a run with no budget left was told to keep working: %s", n)
	}
}

// Every accessor has to tolerate absence: headless and test paths install no
// tracker, and the decision runs at the end of EVERY run.
func TestContinuationIsInertWithoutATracker(t *testing.T) {
	if n := continuationNudge(context.Background()); n != "" {
		t.Fatalf("a bare context produced a nudge: %s", n)
	}
	if n := continuationNudge(nil); n != "" { //nolint:staticcheck // nil ctx is the absence case
		t.Fatalf("a nil context produced a nudge: %s", n)
	}
	var tt *turnTracker
	tt.ObserveTurn(llm.Turn{FinishReason: "length"})
	if tt.lastTurn().Truncated() || tt.truncatedTurns() != 0 {
		t.Fatal("a nil tracker reported state")
	}
}

// The tracker is what makes the run record honest about truncation, so it has
// to count what it saw.
func TestTheTrackerCountsTruncatedTurns(t *testing.T) {
	_, tt := trackerCtx(
		llm.Turn{Agent: einoOrchestratorName, FinishReason: "length"},
		llm.Turn{Agent: einoOrchestratorName, FinishReason: "stop"},
		llm.Turn{Agent: "writer", FinishReason: "length"},
	)
	if got := tt.truncatedTurns(); got != 2 {
		t.Fatalf("counted %d truncated turns, want 2", got)
	}
	if tt.lastTurn().FinishReason != "stop" {
		t.Fatalf("last orchestrator turn = %q, want the stop turn", tt.lastTurn().FinishReason)
	}
}
