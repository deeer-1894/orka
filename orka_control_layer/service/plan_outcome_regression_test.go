package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

func TestRecordedSLAPlanUpdatesExposeOmittedObligations(t *testing.T) {
	data, err := os.ReadFile("testdata/sla_plan_updates.json")
	if err != nil {
		t.Fatal(err)
	}
	var updates []map[string]any
	if err = json.Unmarshal(data, &updates); err != nil {
		t.Fatal(err)
	}
	tracker := &planTracker{}
	ctx := withPlanTracker(context.Background(), tracker)
	var response string
	for _, args := range updates {
		response, err = (planTool{}).Invoke(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"生成 dashboard.html 与 report.md", "研究 GitHub/GitLab Issues API 文档并生成 sources.json", "在子目录复跑 audit.py 比对输出并完成验收"}
	if got := tracker.unfinished(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded residual obligations = %v", got)
	}
	assertPlanFeedback := func(response string) {
		t.Helper()
		var feedback struct {
			Steps      []messages.PlanStep `json:"steps"`
			Unfinished []string            `json:"unfinished"`
			Omitted    []string            `json:"omitted_unfinished"`
		}
		if err := json.Unmarshal([]byte(response), &feedback); err != nil {
			t.Fatalf("plan response hides authoritative state: %s", response)
		}
		if !reflect.DeepEqual(feedback.Steps, tracker.snapshot()) || !reflect.DeepEqual(feedback.Unfinished, want) || !reflect.DeepEqual(feedback.Omitted, want) {
			t.Fatalf("model cannot reconcile omitted originals: %+v", feedback)
		}
	}
	assertPlanFeedback(response)
	// Reposts and empty updates cannot make the reminder disappear.
	for _, args := range []map[string]any{updates[len(updates)-1], {"steps": []any{}}} {
		response, err = (planTool{}).Invoke(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		assertPlanFeedback(response)
	}
	// Recovery must preserve original obligations even though every renamed step is done.
	cp := checkpointFrom(ctx)
	restored := &planTracker{}
	restoreCheckpoint(cp, nil, restored, nil)
	if !reflect.DeepEqual(restored.unfinished(), want) {
		t.Fatal("checkpoint lost original obligations")
	}
	// After verifying the original work, the caller can close its exact original
	// titles. No fuzzy matching, omission, or replacement is treated as completion.
	var closure []any
	for _, title := range want {
		closure = append(closure, map[string]any{"title": title, "status": "done"})
	}
	if _, err = (planTool{}).Invoke(ctx, map[string]any{"steps": closure}); err != nil {
		t.Fatal(err)
	}
	if pending := tracker.unfinished(); len(pending) != 0 {
		t.Fatal(pending)
	}
	if len(tracker.snapshot()) != 12 {
		t.Fatal("closing originals erased plan history")
	}
}

func TestDeliveryCheckExposesUnfinishedPlanWithoutFailingValidFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.md"), []byte("A generated report."), 0600); err != nil {
		t.Fatal(err)
	}
	plan := &planTracker{}
	plan.record([]messages.PlanStep{{Title: "Verify original acceptance conditions", Status: "active"}})
	delivery := newDeliveryTracker(root)
	if err := delivery.declare([]string{"report.md"}); err != nil {
		t.Fatal(err)
	}
	ctx := withDelivery(withPlanTracker(context.Background(), plan), delivery)
	for _, done := range []bool{false, true} {
		if done {
			plan.record([]messages.PlanStep{{Title: "Verify original acceptance conditions", Status: "done"}})
		}
		response, err := (deliveryCheckTool{}).Invoke(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			OK           bool     `json:"ok"`
			PlanComplete *bool    `json:"plan_complete"`
			Unfinished   []string `json:"unfinished_plan"`
		}
		if err := json.Unmarshal([]byte(response), &result); err != nil {
			t.Fatal(err)
		}
		if !result.OK || result.PlanComplete == nil || *result.PlanComplete != done || (!done && len(result.Unfinished) != 1) || (done && len(result.Unfinished) != 0) {
			t.Fatalf("file success confused with task completion: %s", response)
		}
	}
}

func TestRunUnfinishedPlanEmitsPartialAndCorrection(t *testing.T) {
	svc, _ := testService(t, llm.NewMock(
		llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "p", Name: planToolName, Arguments: `{"steps":[{"title":"Verify original requirement","status":"pending"}]}`}}},
		llm.Response{FinishReason: "stop", Content: "All done."},
	))
	col := &collector{}
	status := svc.Run(context.Background(), ChatRunRequest{Message: "go", ConversationID: "plan-outcome"}, col.sink)
	events := col.byType(messages.EventTask)
	if status != db.RunPartial || len(events) != 2 || events[len(events)-1].Action != status {
		t.Fatalf("event/record disagreement: status=%s tasks=%+v", status, events)
	}
	chats := col.byType(messages.EventChat)
	if len(chats) == 0 || !strings.Contains(chats[len(chats)-1].Content, "Verify original requirement") {
		t.Fatalf("no correction after premature all-done claim: %+v", chats)
	}
}

func TestNormalTerminalOutcomeIsFrozenAtPublication(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "done", true: "partial"}[pending], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			plan := &planTracker{}
			want := db.RunDone
			if pending {
				plan.record([]messages.PlanStep{{Title: "original", Status: "pending"}})
				want = db.RunPartial
			}
			rc := &agent.RunContext{Ctx: withPlanTracker(ctx, plan), Vars: map[string]any{}}
			middlewares.SetFinal(rc, "All done.")
			svc, _ := testService(t, llm.NewMock())
			col := &collector{}
			svc.finish(ctx, rc, messages.Meta{}, ChatRunRequest{}, func(m messages.Message) {
				col.sink(m)
				if m.Type == messages.EventTask {
					cancel()
				}
			}, nil)
			status := svc.finalizeRun("", rc, 0, ChatRunRequest{}, nil, ctx.Err())
			events := col.byType(messages.EventTask)
			if len(events) != 1 || events[0].Action != want || status != want {
				t.Fatalf("published terminal changed: expected=%s record=%s events=%+v", want, status, events)
			}
		})
	}
}

func TestNormalPartialCancellationBeforePublicationWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	plan := &planTracker{}
	plan.record([]messages.PlanStep{{Title: "original", Status: "pending"}})
	rc := &agent.RunContext{Ctx: withPlanTracker(ctx, plan), Vars: map[string]any{}}
	svc, _ := testService(t, llm.NewMock())
	col := &collector{}
	svc.finish(ctx, rc, messages.Meta{}, ChatRunRequest{}, func(m messages.Message) {
		col.sink(m)
		if m.Type == messages.EventChat {
			cancel()
		}
	}, nil)
	status := svc.finalizeRun("", rc, 0, ChatRunRequest{}, nil, ctx.Err())
	events := col.byType(messages.EventTask)
	if len(events) != 1 || events[0].Action != db.RunFailed || events[0].Content != "cancelled" || status != db.RunFailed {
		t.Fatalf("late pre-publication cancellation lost: status=%s events=%+v", status, events)
	}
}
