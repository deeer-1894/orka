package service

import (
	"context"
	"sync"
	"testing"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// toolSeeingLLM records which tools each model call was offered.
type toolSeeingLLM struct {
	llm.Client
	mu    sync.Mutex
	calls [][]string
}

func (l *toolSeeingLLM) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	names := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		names = append(names, t.Name)
	}
	l.mu.Lock()
	l.calls = append(l.calls, names)
	l.mu.Unlock()
	return l.Client.Chat(ctx, req)
}

func (l *toolSeeingLLM) offered(call int, tool string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, name := range l.calls[call] {
		if name == tool {
			return true
		}
	}
	return false
}

func TestPlanReviewPausesOnThePlanAndHoldsTheRunToTheApprovedSteps(t *testing.T) {
	model := &toolSeeingLLM{Client: llm.NewMock(
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "p1", Name: planToolName, Arguments: `{"steps":[{"title":"搜索资料","status":"pending"},{"title":"写报告","status":"pending"}]}`}}, FinishReason: "tool_calls"},
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "w1", Name: "file_write", Arguments: `{"path":"report.md"}`}}, FinishReason: "tool_calls"},
		llm.Response{Content: "done", FinishReason: "stop"},
	)}
	svc, _ := testService(t, model)
	writes := 0
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{
			retrievalFixture{"file_read", func(context.Context, map[string]any) (string, error) { return "text", nil }},
			retrievalFixture{"file_write", func(context.Context, map[string]any) (string, error) { writes++; return "written", nil }},
		}, nil, nil
	}

	first := &collector{}
	svc.Run(context.Background(), ChatRunRequest{Message: "写一份报告", ConversationID: "c1", PlanReview: true}, first.sink)
	if !model.offered(0, "file_read") || model.offered(0, "file_write") {
		t.Fatalf("review pass must see read-only tools only, got %v", model.calls[0])
	}
	var pause messages.ClarifyMessage
	for _, m := range first.byType(messages.EventClarify) {
		pause, _ = m.Payload.(messages.ClarifyMessage)
	}
	if pause.Kind != planReviewKind || pause.ResumeKey == "" {
		t.Fatalf("expected a plan-review pause, got %+v", first.msgs)
	}
	if writes != 0 {
		t.Fatal("nothing may be written before the plan is approved")
	}

	second := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "已确认计划", ConversationID: "c1", ResumeKey: pause.ResumeKey,
		ApprovedPlan: []string{"写报告", " ", "写报告", "检查格式"},
	}, second.sink)
	if writes != 1 || !model.offered(1, "file_write") {
		t.Fatalf("approved run must get its tools back: writes=%d tools=%v", writes, model.calls[1])
	}
	plans := second.byType(messages.EventPlan)
	if len(plans) == 0 {
		t.Fatal("the approved plan must be shown")
	}
	approved := plans[0].Payload.(messages.PlanUpdate).Steps
	if len(approved) != 2 || approved[0].Title != "写报告" || approved[1].Title != "检查格式" || approved[0].Status != "pending" {
		t.Fatalf("approved plan = %+v, want the user's two steps, pending", approved)
	}
}

func TestPlanReviewNeverPausesUnattendedRuns(t *testing.T) {
	if planReviewActive(ChatRunRequest{PlanReview: true, Trigger: "schedule"}) {
		t.Fatal("a scheduled run has nobody to approve its plan")
	}
	if planReviewActive(ChatRunRequest{PlanReview: true, ResumeKey: "k"}) {
		t.Fatal("a resumed run is past review")
	}
	if !planReviewActive(ChatRunRequest{PlanReview: true}) {
		t.Fatal("an interactive first pass is reviewed")
	}
}
