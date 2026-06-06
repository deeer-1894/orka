package scheduled_task

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cavis-oss/cavis_control_layer/db"
)

// Source supplies the tasks that are due to run.
type Source func(ctx context.Context) ([]db.TaskMeta, error)

// Trigger runs one task with the rendered prompt content.
type Trigger func(ctx context.Context, task db.TaskMeta, content string) error

// Scheduler periodically scans due tasks, renders their prompt template from
// Variables["prompt_template"], and triggers a chat run.
type Scheduler struct {
	Source   Source
	Trigger  Trigger
	Interval time.Duration
	Log      *slog.Logger
}

// RunDue processes all currently-due tasks once. Returns the number triggered.
func (s *Scheduler) RunDue(ctx context.Context) (int, error) {
	tasks, err := s.Source(ctx)
	if err != nil {
		return 0, fmt.Errorf("scheduler source: %w", err)
	}
	n := 0
	for _, t := range tasks {
		tmpl, _ := t.Variables["prompt_template"].(string)
		content := Render(tmpl, t.Variables)
		if err := s.Trigger(ctx, t, content); err != nil {
			if s.Log != nil {
				s.Log.Error("trigger task", "task_id", t.TaskID, "err", err)
			}
			continue
		}
		n++
	}
	return n, nil
}

// Start runs RunDue on a ticker until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.RunDue(ctx); err != nil && s.Log != nil {
				s.Log.Error("run due", "err", err)
			}
		}
	}
}

// CronSource returns a Source backed by storage tasks with cron_status == "on".
func CronSource(store *db.Storage) Source {
	return func(ctx context.Context) ([]db.TaskMeta, error) {
		return store.ListTasks(ctx, map[string]any{"cron_status": "on"}, 0, 1000)
	}
}
