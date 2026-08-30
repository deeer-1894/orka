package service

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/messages"
)

// These exercise the WIRING, not the budget logic (run_budget_test.go covers
// that): the budget and the plan tracker live on the run context, and the value
// of the whole feature is that finalizeRun reads them back. Run() returns the
// status it filed, so the assertion is on the verdict the platform records.

// A run that finishes everything it declared is a plain success.
func TestRunStatus_CompletePlanIsDone(t *testing.T) {
	svc, _ := testService(t, llm.NewMock(
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "1", Name: planToolName, Arguments: `{"steps":[{"title":"读研报","status":"done"},{"title":"抽因子","status":"done"}]}`}},
			FinishReason: "tool_calls",
		},
		llm.Response{Content: "两步都完成了。", FinishReason: "stop"},
	))
	if got := svc.Run(context.Background(), ChatRunRequest{Message: "go", ConversationID: "c-done"}, func(messages.Message) {}); got != db.RunDone {
		t.Fatalf("status = %q, want %q", got, db.RunDone)
	}
}

// The case this whole change exists for: the agent publishes a checklist, leaves
// steps unfinished, and then writes a confident closing paragraph. Before, that
// was filed as "done" — indistinguishable from a run that actually delivered.
func TestRunStatus_UnfinishedPlanIsPartial(t *testing.T) {
	svc, _ := testService(t, llm.NewMock(
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "1", Name: planToolName, Arguments: `{"steps":[{"title":"读研报","status":"done"},{"title":"抽因子","status":"active"},{"title":"回测","status":"pending"}]}`}},
			FinishReason: "tool_calls",
		},
		llm.Response{Content: "分析完成,结论是买入。", FinishReason: "stop"},
	))
	if got := svc.Run(context.Background(), ChatRunRequest{Message: "go", ConversationID: "c-partial"}, func(messages.Message) {}); got != db.RunPartial {
		t.Fatalf("status = %q, want %q — a run that skipped 2 of its own 3 steps is not a success", got, db.RunPartial)
	}
}

// No published plan means no claim to check, so an ordinary short answer must
// still be a success — the completion check must not punish runs that never
// needed a checklist.
func TestRunStatus_NoPlanIsDone(t *testing.T) {
	svc, _ := testService(t, llm.NewMock(llm.Response{Content: "42", FinishReason: "stop"}))
	if got := svc.Run(context.Background(), ChatRunRequest{Message: "hi", ConversationID: "c-noplan"}, func(messages.Message) {}); got != db.RunDone {
		t.Fatalf("status = %q, want %q", got, db.RunDone)
	}
}
