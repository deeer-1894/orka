package scheduled_task

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/messages"
)

// Source supplies due task snapshots. Claim must compare the snapshot's due
// time with durable state before allowing a run.
type Source func(context.Context) ([]db.TaskMeta, error)

// Trigger blocks until the actual run exits, including all cancellation cleanup.
// It must not launch detached work. Return OutcomeError(chat.Run(...)) to retain
// partial and paused outcomes; nil means explicitly completed.
type Trigger func(context.Context, db.TaskMeta, string) error

// Advance is retained for source compatibility only. It cannot authorize work.
// Deprecated: wire Claims; a Scheduler with only Advance fails closed.
type Advance func(context.Context, string, int64) error

type ClaimStore interface {
	ClaimScheduledTask(context.Context, db.TaskMeta, string, int64) (bool, error)
	CompleteScheduledTask(context.Context, db.TaskMeta, string, string, int64) error
	RenewScheduledTask(context.Context, string, string, int64) (bool, error)
}

type Scheduler struct {
	Source        Source
	Trigger       Trigger
	Claims        ClaimStore
	Advance       Advance
	Interval      time.Duration
	RenewInterval time.Duration
	Log           *slog.Logger
	Now           func() time.Time
}

type outcomeError struct{ status string }

func (e outcomeError) Error() string { return "scheduled run ended: " + e.status }

// OutcomeError prevents partial/paused/unknown runs from being reported as done.
func OutcomeError(status string) error {
	if status == db.RunDone {
		return nil
	}
	return outcomeError{status}
}
func resultStatus(err error) string {
	if err == nil {
		return db.RunDone
	}
	var outcome outcomeError
	if errors.As(err, &outcome) {
		switch outcome.status {
		case db.RunPartial, db.RunPaused, db.RunInterrupted:
			return outcome.status
		}
	}
	return db.RunFailed
}
func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// RunDue claims all due tasks and executes different tasks concurrently. It joins
// every trigger before returning. Overlapping periods of an active task are
// skipped by the durable store, not queued. The count is actual trigger attempts.
func (s *Scheduler) RunDue(ctx context.Context) (int, error) {
	if s.Source == nil || s.Trigger == nil || s.Claims == nil {
		return 0, errors.New("scheduler source, synchronous trigger and atomic claim store required")
	}
	tasks, err := s.Source(ctx)
	if err != nil {
		return 0, fmt.Errorf("scheduler source: %w", err)
	}
	type result struct{ err error }
	results := make(chan result, len(tasks))
	n := 0
	var errs error
	for _, task := range tasks {
		if ctx.Err() != nil {
			errs = errors.Join(errs, ctx.Err())
			break
		}
		claimID := "schedule_" + messages.NewID()
		claimed, err := s.Claims.ClaimScheduledTask(ctx, task, claimID, s.now().UnixMilli())
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("claim %s: %w", task.TaskID, err))
			continue
		}
		if !claimed {
			if s.Log != nil {
				s.Log.Debug("schedule skipped: active run or stale due time", "task_id", task.TaskID, "overlap_policy", "skip")
			}
			continue
		}
		n++
		go func(task db.TaskMeta, claimID string) { results <- result{s.execute(ctx, task, claimID)} }(task, claimID)
	}
	for i := 0; i < n; i++ {
		errs = errors.Join(errs, (<-results).err)
	}
	return n, errs
}

// Start lets new periods scan while long runs are active. CAS remains the
// authority across ticks and processes. Shutdown cancels and joins all runs.
func (s *Scheduler) Start(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	timer := time.NewTicker(interval)
	defer timer.Stop()
	var active sync.WaitGroup
	defer active.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			active.Add(1)
			go func() {
				defer active.Done()
				if _, err := s.RunDue(ctx); err != nil && s.Log != nil {
					s.Log.Error("run due", "error", err)
				}
			}()
		}
	}
}

func CronSource(store *db.Storage) Source {
	return func(ctx context.Context) ([]db.TaskMeta, error) {
		return store.ListTasks(ctx, map[string]any{"cron_status": "on", "next_run_at": map[string]any{"$lte": time.Now().UnixMilli()}}, 0, 1000)
	}
}

// execute owns the trigger and its heartbeat as one lifetime. A failed renewal
// cancels the trigger and prevents the old owner from completing a successor.
// Parent cancellation does not stop renewal until the trigger finishes cleanup.
func (s *Scheduler) execute(parent context.Context, task db.TaskMeta, claimID string) (runErr error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := make(chan struct{})
	renewed := make(chan error, 1)
	interval := s.RenewInterval
	if interval <= 0 || interval > db.ScheduleClaimTTL/3 {
		interval = db.ScheduleClaimTTL / 3
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				renewed <- nil
				return
			case <-ticker.C:
				leaseCtx, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				ok, err := s.Claims.RenewScheduledTask(leaseCtx, task.TaskID, claimID, s.now().UnixMilli())
				c()
				if err != nil || !ok {
					if err == nil {
						err = db.ErrScheduleClaimLost
					}
					cancel()
					renewed <- err
					return
				}
			}
		}
	}()
	defer func() {
		if p := recover(); p != nil {
			runErr = fmt.Errorf("trigger panic: %v", p)
		}
		close(stop)
		if err := <-renewed; err != nil {
			runErr = errors.Join(runErr, err)
			return
		}
		finishCtx, c := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer c()
		runErr = errors.Join(runErr, s.Claims.CompleteScheduledTask(finishCtx, task, claimID, resultStatus(runErr), s.now().UnixMilli()))
	}()
	tmpl, _ := task.Variables["prompt_template"].(string)
	return s.Trigger(ctx, task, Render(tmpl, task.Variables))
}
