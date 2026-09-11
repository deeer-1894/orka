package service

import (
	"context"
	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisRequiredDeliverySurvivesPlanRewrite(t *testing.T) {
	p := &planTracker{}
	ctx := withPlanTracker(context.Background(), p)
	p.record([]messages.PlanStep{{Title: "Produce data", Status: "done"}, {Title: "Deliver report and ZIP", Status: "pending"}})
	_, _ = (planTool{}).Invoke(ctx, map[string]any{"steps": []any{map[string]any{"title": "Produce data", "status": "done"}}})
	svc, _ := testService(t, llm.NewMock())
	rc := &agent.RunContext{Ctx: ctx, Vars: map[string]any{}}
	got := svc.finalizeRun("", rc, 0, ChatRunRequest{}, nil, nil)
	if got == db.RunDone {
		t.Fatal("rewriting checklist removes pending report/ZIP and makes finalizer report done without artifact inspection")
	}
}
func TestAnalysisPartialRunRetainsResumeJournal(t *testing.T) {
	j := newRunJournal(t.TempDir(), "analysis-partial", nil)
	for i := 0; i < 6; i++ {
		j.append(schema.UserMessage("completed work"))
	}
	j.flush()
	svc := &ChatService{}
	svc.settleJournal("analysis-partial", j, db.RunPartial)
	if _, err := os.Stat(j.path()); os.IsNotExist(err) {
		t.Fatal("partial run discarded existing resume journal")
	}
}

func TestDeclaredFilesSurvivePlanReplacementAndAreRechecked(t *testing.T) {
	root := t.TempDir()
	ctx := withDelivery(withPlanTracker(context.Background(), &planTracker{}), newDeliveryTracker(root))
	tool := planTool{}
	first := map[string]any{"steps": []any{map[string]any{"title": "Deliver", "status": "pending"}}, "outputs": []any{"report.json", "README.md"}}
	if _, err := tool.Invoke(ctx, first); err != nil {
		t.Fatal(err)
	}
	done := map[string]any{"steps": []any{map[string]any{"title": "Deliver", "status": "done"}}, "outputs": []any{"report.json"}}
	if _, err := tool.Invoke(ctx, done); err != nil {
		t.Fatal(err)
	}
	svc, _ := testService(t, llm.NewMock())
	rc := &agent.RunContext{Ctx: ctx, Vars: map[string]any{}}
	if got := svc.finalizeRun("", rc, 0, ChatRunRequest{}, nil, nil); got != db.RunPartial {
		t.Fatalf("missing outputs marked %s", got)
	}
	os.WriteFile(filepath.Join(root, "report.json"), []byte(`{"value":1}`), 0600)
	os.WriteFile(filepath.Join(root, "README.md"), []byte("reproduce"), 0600)
	result, err := (deliveryCheckTool{}).Invoke(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, `"ok":true`) {
		t.Fatal(result)
	}
	if got := svc.finalizeRun("", rc, 0, ChatRunRequest{}, nil, nil); got != db.RunDone {
		t.Fatalf("valid delivery marked %s", got)
	}
	os.WriteFile(filepath.Join(root, "report.json"), []byte("{"), 0600)
	if got := svc.finalizeRun("", rc, 0, ChatRunRequest{}, nil, nil); got != db.RunPartial {
		t.Fatalf("mutated delivery marked %s", got)
	}
}
