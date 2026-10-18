package scheduled_task

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

func TestSchedulerRejectsAdvanceOnly(t *testing.T) {
	triggered := false
	s := Scheduler{Source: func(context.Context) ([]db.TaskMeta, error) {
		return []db.TaskMeta{{TaskID: "t", IntervalSec: 1, CronStatus: "on"}}, nil
	}, Advance: func(context.Context, string, int64) error { return nil }, Trigger: func(context.Context, db.TaskMeta, string) error { triggered = true; return nil }}
	if _, err := s.RunDue(context.Background()); err == nil || triggered {
		t.Fatal("unsafe Advance-only scheduler was allowed to run")
	}
}

// fakeClaims models a shared durable CAS store across independent schedulers.
type fakeClaims struct {
	mu        sync.Mutex
	task      db.TaskMeta
	active    string
	finishes  int
	skips     int
	failClaim bool
	failRenew bool
	renewals  int
	status    string
}

func (f *fakeClaims) ClaimScheduledTask(_ context.Context, t db.TaskMeta, id string, now int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failClaim {
		return false, errors.New("offline")
	}
	if f.task.NextRunAt != t.NextRunAt {
		return false, nil
	}
	f.task.NextRunAt = now + t.IntervalSec*1000
	if f.active != "" {
		f.skips++
		return false, nil
	}
	f.active = id
	return true, nil
}
func (f *fakeClaims) CompleteScheduledTask(ctx context.Context, t db.TaskMeta, id, status string, now int64) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active != id {
		return errors.New("stale owner")
	}
	f.finishes++
	f.active = ""
	f.status = status
	return nil
}
func (f *fakeClaims) source(context.Context) ([]db.TaskMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []db.TaskMeta{f.task}, nil
}
func newClaims() *fakeClaims {
	return &fakeClaims{task: db.TaskMeta{TaskID: "task", CronStatus: "on", IntervalSec: 1, NextRunAt: 1, Variables: map[string]any{"prompt_template": "hi {{name}}", "name": "Sam"}}}
}

func TestSchedulerTwoInstancesAndCrossPeriodDoNotReenter(t *testing.T) {
	claims := newClaims()
	entered := make(chan struct{})
	release := make(chan struct{})
	s := Scheduler{Claims: claims, Source: claims.source, Trigger: func(_ context.Context, _ db.TaskMeta, content string) error {
		if content != "hi Sam" {
			t.Errorf("render: %s", content)
		}
		close(entered)
		<-release
		return nil
	}, Now: func() time.Time { return time.UnixMilli(10000) }}
	done := make(chan error, 1)
	go func() { _, err := s.RunDue(context.Background()); done <- err }()
	<-entered
	// A second process sees a later due period while the first run is still active.
	other := s
	other.Now = func() time.Time { return time.UnixMilli(12000) }
	n, err := other.RunDue(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("overlap triggered: %d %v", n, err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if claims.finishes != 1 || claims.skips != 1 {
		t.Fatalf("finishes=%d skips=%d", claims.finishes, claims.skips)
	}
}
func TestSchedulerReleasesFailedOrCancelledRun(t *testing.T) {
	for _, status := range []string{db.RunFailed, db.RunPaused, db.RunPartial} {
		t.Run(status, func(t *testing.T) {
			claims := newClaims()
			ctx, cancel := context.WithCancel(context.Background())
			s := Scheduler{Claims: claims, Source: claims.source, Trigger: func(context.Context, db.TaskMeta, string) error { cancel(); return OutcomeError(status) }}
			n, err := s.RunDue(ctx)
			if n != 1 || err == nil || claims.active != "" || claims.status != status {
				t.Fatalf("n=%d err=%v active=%q status=%s", n, err, claims.active, claims.status)
			}
		})
	}
}
func TestSchedulerClaimsFailClosed(t *testing.T) {
	claims := newClaims()
	claims.failClaim = true
	s := Scheduler{Claims: claims, Source: claims.source, Trigger: func(context.Context, db.TaskMeta, string) error { t.Error("triggered without claim"); return nil }}
	if n, err := s.RunDue(context.Background()); n != 0 || err == nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
func TestSchedulerOtherTasksDoNotWaitForSlowTask(t *testing.T) {
	first, second := newClaims(), newClaims()
	second.task.TaskID = "second"
	claims := &twoClaims{first, second}
	started := make(chan string, 2)
	release := make(chan struct{})
	s := Scheduler{Claims: claims, Source: func(context.Context) ([]db.TaskMeta, error) { return []db.TaskMeta{first.task, second.task}, nil }, Trigger: func(_ context.Context, t db.TaskMeta, _ string) error { started <- t.TaskID; <-release; return nil }}
	done := make(chan struct{})
	go func() { s.RunDue(context.Background()); close(done) }()
	<-started
	<-started
	close(release)
	<-done
}

type twoClaims struct{ first, second *fakeClaims }

func (f *twoClaims) ClaimScheduledTask(c context.Context, t db.TaskMeta, id string, n int64) (bool, error) {
	if t.TaskID == "second" {
		return f.second.ClaimScheduledTask(c, t, id, n)
	}
	return f.first.ClaimScheduledTask(c, t, id, n)
}
func (f *twoClaims) CompleteScheduledTask(c context.Context, t db.TaskMeta, id, status string, n int64) error {
	if t.TaskID == "second" {
		return f.second.CompleteScheduledTask(c, t, id, status, n)
	}
	return f.first.CompleteScheduledTask(c, t, id, status, n)
}

func (f *fakeClaims) RenewScheduledTask(ctx context.Context, taskID, id string, now int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewals++
	if f.failRenew {
		return false, errors.New("renewal unavailable")
	}
	return f.active == id, nil
}
func (f *twoClaims) RenewScheduledTask(c context.Context, taskID, id string, n int64) (bool, error) {
	if taskID == "second" {
		return f.second.RenewScheduledTask(c, taskID, id, n)
	}
	return f.first.RenewScheduledTask(c, taskID, id, n)
}
func TestSchedulerRenewsLongRunAndCancelsOnLostLease(t *testing.T) {
	claims := newClaims()
	started := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := Scheduler{Claims: claims, Source: claims.source, RenewInterval: time.Millisecond, Trigger: func(ctx context.Context, _ db.TaskMeta, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	done := make(chan error, 1)
	go func() { _, err := s.RunDue(ctx); done <- err }()
	<-started
	// Wait for actual renewal, not a guessed execution duration.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		claims.mu.Lock()
		renewed := claims.renewals > 0
		claims.mu.Unlock()
		if renewed {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("long task never renewed")
		}
	}
	claims.mu.Lock()
	claims.failRenew = true
	claims.mu.Unlock()
	if err := <-done; err == nil {
		t.Fatal("lost lease not reported")
	}
	if ctx.Err() != nil {
		t.Fatal("task only cancelled by test deadline")
	}
	claims.mu.Lock()
	defer claims.mu.Unlock()
	if claims.finishes != 0 || claims.active == "" {
		t.Fatal("lost owner completed/released a claim")
	}
}

func TestSchedulerKeepsRenewingWhileCancelledTriggerCleansUp(t *testing.T) {
	claims := newClaims()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleaning := make(chan struct{})
	release := make(chan struct{})
	s := Scheduler{Claims: claims, Source: claims.source, RenewInterval: time.Millisecond, Trigger: func(ctx context.Context, _ db.TaskMeta, _ string) error {
		cancel()
		<-ctx.Done()
		close(cleaning)
		<-release
		return ctx.Err()
	}}
	done := make(chan struct{})
	go func() { s.RunDue(ctx); close(done) }()
	<-cleaning
	defer func() { close(release); <-done }()
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		claims.mu.Lock()
		renewed := claims.renewals > 0
		claims.mu.Unlock()
		if renewed {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("cancelled task lost heartbeat before cleanup finished")
		}
	}
}
