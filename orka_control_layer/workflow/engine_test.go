package workflow

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
)

type memoryStore struct {
	mu     sync.Mutex
	states []db.WorkflowRun
	failAt int
}

func (s *memoryStore) SaveWorkflowRun(_ context.Context, r db.WorkflowRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAt > 0 && len(s.states)+1 == s.failAt {
		return errors.New("storage unavailable")
	}
	r.Steps = append([]db.WorkflowStepRun(nil), r.Steps...)
	s.states = append(s.states, r)
	return nil
}
func TestValidate(t *testing.T) {
	for name, steps := range map[string][]db.WorkflowStep{
		"empty":     nil,
		"blank":     {{Name: " "}},
		"duplicate": {{Name: "a"}, {Name: "a"}},
		"missing":   {{Name: "a", DependsOn: []string{"b"}}},
		"self":      {{Name: "a", DependsOn: []string{"a"}}},
		"cycle":     {{Name: "a", DependsOn: []string{"b"}}, {Name: "b", DependsOn: []string{"a"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if Validate(steps) == nil {
				t.Fatal("invalid DAG accepted")
			}
		})
	}
	if err := Validate([]db.WorkflowStep{{Name: "a"}, {Name: "b", DependsOn: []string{"a"}}}); err != nil {
		t.Fatal(err)
	}
}
func TestIncompleteBlocksDependents(t *testing.T) {
	for _, status := range []string{db.RunPartial, db.RunPaused, db.RunFailed, "unknown"} {
		t.Run(status, func(t *testing.T) {
			store := &memoryStore{}
			called := []string{}
			engine := Engine{Store: store, RunStep: func(_ context.Context, e Execution) Result {
				called = append(called, e.Step.Name)
				return Result{Status: status, Output: "unfinished"}
			}}
			r, err := engine.Run(context.Background(), db.Workflow{Steps: []db.WorkflowStep{{Name: "a", OnError: "continue"}, {Name: "b", DependsOn: []string{"a"}}}}, "conv", "parent")
			if err != nil {
				t.Fatal(err)
			}
			if len(called) != 1 || called[0] != "a" {
				t.Fatalf("downstream ran: %v", called)
			}
			if r.Status == db.RunDone || r.Steps[1].Status != Blocked {
				t.Fatalf("incomplete run: %+v", r)
			}
			if len(store.states) < 3 || store.states[len(store.states)-1].Status != r.Status {
				t.Fatal("terminal status not persisted")
			}
		})
	}
}
func TestParallelIdentityAndCancellation(t *testing.T) {
	store := &memoryStore{}
	entered := make(chan Execution, 2)
	release := make(chan struct{})
	engine := Engine{Store: store, RunStep: func(ctx context.Context, e Execution) Result {
		if e.Step.Name == "root" {
			return Result{Status: db.RunDone}
		}
		entered <- e
		select {
		case <-release:
		case <-ctx.Done():
		}
		return Result{Status: db.RunPaused}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan db.WorkflowRun, 1)
	go func() {
		r, _ := engine.Run(ctx, db.Workflow{Steps: []db.WorkflowStep{{Name: "root"}, {Name: "a", DependsOn: []string{"root"}}, {Name: "b", DependsOn: []string{"root"}}}}, "conv", "parent")
		done <- r
	}()
	a, b := <-entered, <-entered
	if a.ExecutionID == b.ExecutionID || a.ExecutionID == "" || a.ParentExecutionID != "parent" || b.ParentExecutionID != "parent" {
		t.Fatal("execution identities collide")
	}
	cancel()
	r := <-done
	if r.Status == db.RunDone {
		t.Fatal("cancelled parent succeeded")
	}
}
func TestPersistenceFailurePreventsExecution(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		calls := 0
		engine := Engine{Store: &memoryStore{failAt: failAt}, RunStep: func(context.Context, Execution) Result { calls++; return Result{Status: db.RunDone} }}
		if _, err := engine.Run(context.Background(), db.Workflow{Steps: []db.WorkflowStep{{Name: "a"}}}, "conv", "parent"); err == nil {
			t.Fatal("storage error hidden")
		}
		if calls != 0 {
			t.Fatal("executed without durable running status")
		}
	}
}
func TestConditionalSkipAndContinue(t *testing.T) {
	store := &memoryStore{}
	called := map[string]bool{}
	var mu sync.Mutex
	engine := Engine{Store: store, ShouldRun: func(e Execution) bool { return e.Step.Name != "skip" }, RunStep: func(_ context.Context, e Execution) Result {
		mu.Lock()
		called[e.Step.Name] = true
		mu.Unlock()
		if e.Step.Name == "bad" {
			return Result{Status: db.RunPartial}
		}
		return Result{Status: db.RunDone}
	}}
	r, err := engine.Run(context.Background(), db.Workflow{Steps: []db.WorkflowStep{{Name: "root"}, {Name: "skip", DependsOn: []string{"root"}}, {Name: "child", DependsOn: []string{"skip"}}, {Name: "bad", OnError: "continue", DependsOn: []string{"root"}}}}, "conv", "parent")
	if err != nil {
		t.Fatal(err)
	}
	if called["skip"] || !called["child"] || r.Status != db.RunPartial {
		t.Fatalf("wrong conditional/continue behavior: %v %+v", called, r)
	}
}

func TestRetryHasNewIdentityAndDoesNotRetryPaused(t *testing.T) {
	for _, status := range []string{db.RunFailed, db.RunPaused, db.RunPartial} {
		t.Run(status, func(t *testing.T) {
			var ids []string
			engine := Engine{Store: &memoryStore{}, RunStep: func(_ context.Context, e Execution) Result {
				ids = append(ids, e.ExecutionID)
				if e.Attempt == 1 {
					return Result{Status: status}
				}
				return Result{Status: db.RunDone}
			}}
			r, err := engine.Run(context.Background(), db.Workflow{Steps: []db.WorkflowStep{{Name: "a", OnError: "retry:1"}}}, "conv", "parent")
			if err != nil {
				t.Fatal(err)
			}
			if status == db.RunFailed {
				if len(ids) != 2 || ids[0] == ids[1] || r.Status != db.RunDone {
					t.Fatalf("retry not isolated: %v %+v", ids, r)
				}
			} else if len(ids) != 1 || r.Status != status {
				t.Fatalf("incomplete retried: %v %+v", ids, r)
			}
		})
	}
}
func TestStorageFailureReturnsFailedState(t *testing.T) {
	engine := Engine{Store: &memoryStore{failAt: 1}, RunStep: func(context.Context, Execution) Result { return Result{Status: db.RunDone} }}
	r, err := engine.Run(context.Background(), db.Workflow{Steps: []db.WorkflowStep{{Name: "a"}}}, "conv", "parent")
	if err == nil || r.Status != db.RunFailed {
		t.Fatalf("failed start reported running: status=%s err=%v", r.Status, err)
	}
}

func TestFinalPersistenceFailureDoesNotReportDone(t *testing.T) {
	engine := Engine{Store: &memoryStore{failAt: 4}, RunStep: func(context.Context, Execution) Result { return Result{Status: db.RunDone} }}
	r, err := engine.Run(context.Background(), db.Workflow{Steps: []db.WorkflowStep{{Name: "a"}}}, "conv", "parent")
	if err == nil || r.Status == db.RunDone {
		t.Fatalf("failed final persistence reported done: status=%s err=%v", r.Status, err)
	}
}
