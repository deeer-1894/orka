package service

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/schema"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// Replay the observed failure at the real runner boundary: the artifact has
// correct figures, but the final model generation invents a denominator and row.
func TestDeliveryResponseDoesNotRepublishInventedStatistics(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.md"), []byte("P0: 38/43; T0068 source_row=74, closed=true.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := &planTracker{}
	p.record([]messages.PlanStep{{Title: "Write report", Status: "done"}})
	d := newDeliveryTracker(root)
	if err := d.configure([]string{"report.md"}, "file_receipt"); err != nil {
		t.Fatal(err)
	}
	j := newRunJournal(t.TempDir(), "final-receipt", nil)
	ctx := withJournal(withDelivery(withPlanTracker(context.Background(), p), d), j)
	incorrect := "P0=38/42; T0068 source_row=106, open=true."
	model := llm.NewMock(llm.Response{Content: incorrect, FinishReason: "stop"})
	ag, err := BuildEinoOrchestrator(ctx, model, "test", "deliver files", nil, nil, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	rc := &agent.RunContext{Ctx: ctx, Vars: map[string]any{}, Messages: []messages.Message{messages.Chat(messages.RoleUser, "交付报告并给出文件链接", messages.Meta{})}}
	var published strings.Builder
	if err = StreamEinoRun(ctx, rc, ag, func(m messages.Message) { published.WriteString(m.Content) }); err != nil {
		t.Fatal(err)
	}
	for label, got := range map[string]string{"events": published.String(), "persisted final": middlewares.Final(rc)} {
		if strings.Contains(got, "38/42") || strings.Contains(got, "source_row=106") {
			t.Errorf("%s leaked invented final statistics: %s", label, got)
		}
		if !strings.Contains(got, "report.md") {
			t.Errorf("%s missing deliverable link: %s", label, got)
		}
	}
	if model.Calls() != 1 {
		t.Fatalf("delivery added an unbudgeted model call: %d", model.Calls())
	}
}

func TestDeliveryResponsePreservesNonDeliveryAnswers(t *testing.T) {
	for _, scenario := range []string{"ordinary", "pending", "missing", "mutated", "budget", "cancelled", "tool-progress"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			ctx := context.Background()
			d := newDeliveryTracker(root)
			p := &planTracker{}
			p.record([]messages.PlanStep{{Title: "Work", Status: "done"}})
			if scenario != "ordinary" {
				if err := d.configure([]string{"report.json"}, "file_receipt"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "report.json"), []byte(`{"valid":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx = withDelivery(withPlanTracker(ctx, p), d)
			m := schema.AssistantMessage("未解决的限制及用户要求的答复", nil)
			switch scenario {
			case "pending":
				p.record([]messages.PlanStep{{Title: "Work", Status: "pending"}})
			case "missing":
				if err := os.Remove(filepath.Join(root, "report.json")); err != nil {
					t.Fatal(err)
				}
			case "mutated":
				if _, err := (deliveryCheckTool{}).Invoke(ctx, nil); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "report.json"), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "budget":
				b := newRunBudget(100, 100, 0)
				b.AddUsage(100, 0)
				ctx = withBudget(ctx, b)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "tool-progress":
				m.ToolCalls = []schema.ToolCall{{ID: "read", Function: schema.FunctionCall{Name: "file_read", Arguments: `{"path":"report.json"}`}}}
			}
			if got := deliveryResponse(ctx, m); got != m {
				t.Fatalf("%s answer replaced: %s", scenario, got.Content)
			}
		})
	}
}

func TestDeliveryResponseEscapesDownloadLinks(t *testing.T) {
	root := t.TempDir()
	name := "x:y[报告] #1?.md"
	if err := os.WriteFile(filepath.Join(root, name), []byte("report"), 0600); err != nil {
		t.Fatal(err)
	}
	d := newDeliveryTracker(root)
	if err := d.configure([]string{name}, "file_receipt"); err != nil {
		t.Fatal(err)
	}
	p := &planTracker{}
	p.record([]messages.PlanStep{{Title: "Deliver", Status: "done"}})
	ctx := withDelivery(withPlanTracker(context.Background(), p), d)
	got := deliveryResponse(ctx, schema.AssistantMessage("done", nil)).Content
	if !strings.Contains(got, "(./x:y%5B%E6%8A%A5%E5%91%8A%5D%20%231%3F.md)") {
		t.Fatal(got)
	}
	if bufferDeliveryContent(ctx, "worker") {
		t.Fatal("delegate streaming suppressed")
	}
	if !bufferDeliveryContent(ctx, "") {
		t.Fatal("main final leaks before checking")
	}
	if bufferDeliveryContent(context.Background(), "") {
		t.Fatal("ordinary chat streaming suppressed")
	}
}

func TestDeliveryResponseRequiresExplicitReceiptMode(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.md"), []byte("report"), 0600); err != nil {
		t.Fatal(err)
	}
	d := newDeliveryTracker(root)
	if err := d.declare([]string{"report.md"}); err != nil {
		t.Fatal(err)
	}
	p := &planTracker{}
	p.record([]messages.PlanStep{{Title: "Work", Status: "done"}})
	ctx := withDelivery(withPlanTracker(context.Background(), p), d)
	m := schema.AssistantMessage("用户要求聊天里单独回答的问题", nil)
	if got := deliveryResponse(ctx, m); got != m {
		t.Fatal("mere file existence must not replace requested chat answer")
	}
	if bufferDeliveryContent(ctx, "") {
		t.Fatal("ordinary file task must keep streaming")
	}
}

func TestDeliveryResponseModeSurvivesRecoveryAndCanBeChanged(t *testing.T) {
	d := newDeliveryTracker(t.TempDir())
	ctx := withDelivery(withPlanTracker(context.Background(), &planTracker{}), d)
	args := map[string]any{"steps": []any{map[string]any{"title": "Deliver", "status": "pending"}}, "outputs": []any{"report.md"}, "final_response": "file_receipt"}
	if _, err := (planTool{}).Invoke(ctx, args); err != nil {
		t.Fatal(err)
	}
	if _, err := (planTool{}).Invoke(ctx, map[string]any{"steps": []any{}}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(checkpointFrom(ctx))
	if err != nil {
		t.Fatal(err)
	}
	var cp runCheckpoint
	if err = json.Unmarshal(raw, &cp); err != nil {
		t.Fatal(err)
	}
	restored := newDeliveryTracker(t.TempDir())
	restoreCheckpoint(&cp, nil, &planTracker{}, restored)
	if restored.responseMode() != "file_receipt" {
		t.Fatal("mode lost during resume")
	}
	if err := restored.configure([]string{"extra.md"}, "invalid"); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if len(restored.snapshot()) != 1 || restored.responseMode() != "file_receipt" {
		t.Fatal("invalid update mutated contract")
	}
	if err := restored.configure([]string{"../escape"}, "answer"); err == nil {
		t.Fatal("invalid path accepted")
	}
	if restored.responseMode() != "file_receipt" {
		t.Fatal("failed declaration changed mode")
	}
	if err := restored.configure(nil, "answer"); err != nil {
		t.Fatal(err)
	}
	if restored.responseMode() != "answer" {
		t.Fatal("user's revised response needs cannot be honored")
	}
	legacy := newDeliveryTracker(t.TempDir())
	restoreCheckpoint(&runCheckpoint{Outputs: []string{"report.md"}}, nil, nil, legacy)
	if legacy.responseMode() != "answer" {
		t.Fatal("legacy checkpoint opted into receipt")
	}
}
