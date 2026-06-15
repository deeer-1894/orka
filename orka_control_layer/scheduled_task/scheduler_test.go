package scheduled_task

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
)

func TestRender(t *testing.T) {
	got := Render("Daily report for {{date}} owned by {{ owner }}", map[string]any{
		"date": "2026-06-06", "owner": "bob",
	})
	if got != "Daily report for 2026-06-06 owned by bob" {
		t.Fatalf("render = %q", got)
	}
	if Render("{{missing}}!", nil) != "!" {
		t.Fatalf("unknown key not blanked")
	}
}

func TestScheduler_RunDueTriggersAndRenders(t *testing.T) {
	tasks := []db.TaskMeta{
		{TaskID: "t1", CronStatus: "on", Variables: map[string]any{
			"prompt_template": "summarize sales for {{region}}", "region": "APAC",
		}},
		{TaskID: "t2", CronStatus: "on", Variables: map[string]any{
			"prompt_template": "no vars here",
		}},
	}
	var triggered []string
	s := &Scheduler{
		Source:  func(_ context.Context) ([]db.TaskMeta, error) { return tasks, nil },
		Trigger: func(_ context.Context, task db.TaskMeta, content string) error {
			triggered = append(triggered, task.TaskID+"="+content)
			return nil
		},
	}
	n, err := s.RunDue(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if triggered[0] != "t1=summarize sales for APAC" {
		t.Fatalf("t1 render wrong: %q", triggered[0])
	}
	if triggered[1] != "t2=no vars here" {
		t.Fatalf("t2 render wrong: %q", triggered[1])
	}
}

// TestScheduler_ClaimsBeforeTrigger verifies the idempotency contract: an
// interval task's next_run_at is advanced BEFORE it is triggered, and a failed
// claim skips the trigger entirely (so the task can't be double-fired).
func TestScheduler_ClaimsBeforeTrigger(t *testing.T) {
	var order []string
	s := &Scheduler{
		Source: func(_ context.Context) ([]db.TaskMeta, error) {
			return []db.TaskMeta{{TaskID: "t1", CronStatus: "on", IntervalSec: 60,
				Variables: map[string]any{"prompt_template": "go"}}}, nil
		},
		Advance: func(_ context.Context, id string, _ int64) error { order = append(order, "advance:"+id); return nil },
		Trigger: func(_ context.Context, task db.TaskMeta, _ string) error { order = append(order, "trigger:"+task.TaskID); return nil },
	}
	if _, err := s.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "advance:t1" || order[1] != "trigger:t1" {
		t.Fatalf("expected advance before trigger, got %v", order)
	}

	// A failed claim must NOT trigger.
	var triggered bool
	fail := &Scheduler{
		Source: func(_ context.Context) ([]db.TaskMeta, error) {
			return []db.TaskMeta{{TaskID: "t2", CronStatus: "on", IntervalSec: 60, Variables: map[string]any{}}}, nil
		},
		Advance: func(_ context.Context, _ string, _ int64) error { return context.DeadlineExceeded },
		Trigger: func(_ context.Context, _ db.TaskMeta, _ string) error { triggered = true; return nil },
	}
	n, _ := fail.RunDue(context.Background())
	if triggered || n != 0 {
		t.Fatalf("failed claim should skip trigger; triggered=%v n=%d", triggered, n)
	}
}
