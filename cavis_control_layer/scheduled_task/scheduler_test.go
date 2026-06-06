package scheduled_task

import (
	"context"
	"testing"

	"github.com/cavis-oss/cavis_control_layer/db"
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
