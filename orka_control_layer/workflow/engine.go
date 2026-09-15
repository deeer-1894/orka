// Package workflow owns DAG validation and durable execution state. It has no
// dependency on chat, model clients, HTTP, or a particular storage implementation.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/messages"
)

const (
	Pending   = "pending"
	Skipped   = "skipped"
	Blocked   = "blocked"
	Cancelled = "cancelled"
)

type Store interface {
	SaveWorkflowRun(context.Context, db.WorkflowRun) error
}
type Execution struct {
	Step                           db.WorkflowStep
	Prior                          map[string]string
	ExecutionID, ParentExecutionID string
	Attempt                        int
}
type Result struct{ Status, Output, Error string }
type Engine struct {
	Store      Store
	RunStep    func(context.Context, Execution) Result
	ShouldRun  func(Execution) bool
	RetryDelay time.Duration
}

// Normalize preserves legacy sequential workflows when no dependencies exist.
func Normalize(steps []db.WorkflowStep) []db.WorkflowStep {
	out := append([]db.WorkflowStep(nil), steps...)
	explicit := false
	for _, st := range out {
		if len(st.DependsOn) > 0 {
			explicit = true
		}
	}
	if !explicit {
		for i := 1; i < len(out); i++ {
			out[i].DependsOn = []string{out[i-1].Name}
		}
	}
	return out
}

func Validate(steps []db.WorkflowStep) error {
	if len(steps) == 0 {
		return errors.New("at least one step required")
	}
	names := make(map[string]int, len(steps))
	for i, st := range steps {
		if strings.TrimSpace(st.Name) == "" {
			return errors.New("step name required")
		}
		if _, ok := names[st.Name]; ok {
			return fmt.Errorf("duplicate step %q", st.Name)
		}
		names[st.Name] = i
	}
	steps = Normalize(steps)
	colors := make([]uint8, len(steps))
	var visit func(int) error
	visit = func(i int) error {
		if colors[i] == 1 {
			return fmt.Errorf("workflow cycle at %q", steps[i].Name)
		}
		if colors[i] == 2 {
			return nil
		}
		colors[i] = 1
		for _, dep := range steps[i].DependsOn {
			j, ok := names[dep]
			if !ok {
				return fmt.Errorf("step %q depends on missing step %q", steps[i].Name, dep)
			}
			if err := visit(j); err != nil {
				return err
			}
		}
		colors[i] = 2
		return nil
	}
	for i := range steps {
		if err := visit(i); err != nil {
			return err
		}
	}
	return nil
}

func NewRun(wf db.Workflow, convID, runID string) db.WorkflowRun {
	r := db.WorkflowRun{RunID: runID, WorkflowID: wf.WorkflowID, OwnerEmail: wf.OwnerEmail, ConversationID: convID, Status: db.RunRunning, Definition: Normalize(wf.Steps), CreatedAt: time.Now().UnixMilli()}
	for _, st := range r.Definition {
		r.Steps = append(r.Steps, db.WorkflowStepRun{Name: st.Name, Status: Pending})
	}
	return r
}

func retries(policy string) int {
	p := strings.ToLower(strings.TrimSpace(policy))
	if !strings.HasPrefix(p, "retry:") {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(p, "retry:")))
	if n < 0 {
		return 0
	}
	return n
}
func continues(policy string) bool { return strings.EqualFold(strings.TrimSpace(policy), "continue") }
func complete(status string) bool  { return status == db.RunDone || status == Skipped }

// Run persists each wave before dispatching work and every result before it can
// unlock dependents. Partial/paused never unlock dependencies or auto-retry.
func (e Engine) Run(parent context.Context, wf db.Workflow, convID, runID string) (db.WorkflowRun, error) {
	r := NewRun(wf, convID, runID)
	if err := Validate(wf.Steps); err != nil {
		return r, err
	}
	if e.Store == nil || e.RunStep == nil || runID == "" {
		return r, errors.New("workflow store, executor and run ID required")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	save := func() error {
		persist, c := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer c()
		return e.Store.SaveWorkflowRun(persist, r)
	}
	if err := save(); err != nil {
		r.Status = db.RunFailed
		r.Error = err.Error()
		r.FinishedAt = time.Now().UnixMilli()
		return r, err
	}
	indices := map[string]int{}
	for i, st := range r.Definition {
		indices[st.Name] = i
	}
	var runErr error
	stopped := false
	for !stopped && ctx.Err() == nil {
		prior := map[string]string{}
		for _, st := range r.Steps {
			if complete(st.Status) {
				prior[st.Name] = st.Output
			}
		}
		var wave []int
		for i, st := range r.Definition {
			if r.Steps[i].Status != Pending {
				continue
			}
			ready := true
			for _, dep := range st.DependsOn {
				if !complete(r.Steps[indices[dep]].Status) {
					ready = false
					break
				}
			}
			if ready {
				wave = append(wave, i)
			}
		}
		if len(wave) == 0 {
			break
		}
		executions := map[int]Execution{}
		retrying := false
		for _, i := range wave {
			st := &r.Steps[i]
			// Each callback receives its own immutable output snapshot.
			outputs := map[string]string{}
			for k, v := range prior {
				outputs[k] = v
			}
			x := Execution{Step: r.Definition[i], Prior: outputs, ExecutionID: "run_" + messages.NewID(), ParentExecutionID: runID, Attempt: st.Attempts + 1}
			if e.ShouldRun != nil && !e.ShouldRun(x) {
				st.Status = Skipped
				st.Output = "(skipped)"
				st.FinishedAt = time.Now().UnixMilli()
				continue
			}
			retrying = retrying || st.Attempts > 0
			st.Status = db.RunRunning
			st.ExecutionID = x.ExecutionID
			st.Attempts++
			st.StartedAt = time.Now().UnixMilli()
			st.FinishedAt = 0
			executions[i] = x
		}
		if err := save(); err != nil {
			runErr = err
			break
		}
		if retrying && e.RetryDelay > 0 {
			timer := time.NewTimer(e.RetryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			break
		}
		type outcome struct {
			i      int
			result Result
		}
		results := make(chan outcome, len(executions))
		for i, x := range executions {
			go func(i int, x Execution) {
				result := Result{Status: db.RunFailed}
				// A callback panic must still produce a result, cancel siblings, and leave
				// a durable terminal state instead of stranding the parent.
				defer func() {
					if p := recover(); p != nil {
						result = Result{Status: db.RunFailed, Error: fmt.Sprint(p)}
					}
					results <- outcome{i, result}
				}()
				result = e.RunStep(ctx, x)
			}(i, x)
		}
		for range executions {
			got := <-results
			st := &r.Steps[got.i]
			result := got.result
			switch result.Status {
			case db.RunDone, db.RunFailed, db.RunPartial, db.RunPaused, db.RunInterrupted, Cancelled:
			default:
				result.Error = "unexpected step status: " + result.Status
				result.Status = db.RunFailed
			}
			st.Status = result.Status
			st.Output = result.Output
			st.Error = result.Error
			st.FinishedAt = time.Now().UnixMilli()
			policy := r.Definition[got.i].OnError
			if result.Status == db.RunFailed && st.Attempts <= retries(policy) && ctx.Err() == nil && runErr == nil {
				st.Status = Pending
			} else if !complete(result.Status) && !continues(policy) {
				stopped = true
				cancel()
			}
			if err := save(); err != nil {
				runErr = errors.Join(runErr, err)
				stopped = true
				cancel()
			}
		}
	}
	// There are no goroutines left here: cancelled children have been joined.
	r.Status = db.RunDone
	for i := range r.Steps {
		st := &r.Steps[i]
		if st.Status == Pending {
			st.Status = Blocked
			st.Error = "dependency incomplete or workflow stopped"
			st.FinishedAt = time.Now().UnixMilli()
		}
		if st.Status == db.RunRunning {
			st.Status = Cancelled
			st.FinishedAt = time.Now().UnixMilli()
		}
		switch st.Status {
		case db.RunFailed:
			r.Status = db.RunFailed
		case db.RunPaused:
			if r.Status != db.RunFailed {
				r.Status = db.RunPaused
			}
		case db.RunPartial:
			if r.Status == db.RunDone {
				r.Status = db.RunPartial
			}
		case db.RunInterrupted, Cancelled, Blocked:
			if r.Status == db.RunDone {
				r.Status = db.RunPartial
			}
		}
	}
	if parent.Err() != nil && r.Status != db.RunFailed {
		r.Status = Cancelled
	}
	if runErr != nil {
		r.Status = db.RunFailed
		r.Error = runErr.Error()
	}
	r.FinishedAt = time.Now().UnixMilli()
	if err := save(); err != nil {
		runErr = errors.Join(runErr, err)
		r.Status = db.RunFailed
		r.Error = runErr.Error()
	}
	return r, runErr
}
