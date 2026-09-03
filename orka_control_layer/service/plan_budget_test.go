package service

import (
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/messages"
)

// A re-posted checklist carries no information but costs a whole model
// round-trip; consecutive update_plan calls 41-80 seconds apart were measured
// here. same() is what lets the tool answer one without spending an event.
func TestPlanTrackerSame(t *testing.T) {
	p := &planTracker{}
	steps := []messages.PlanStep{{Title: "查资料", Status: "active"}, {Title: "写报告", Status: "pending"}}

	// The first plan of a run is always news, even against an empty tracker.
	if p.same(steps) {
		t.Fatal("an unrecorded plan must not count as unchanged")
	}
	p.record(steps)
	if !p.same(steps) {
		t.Error("an identical re-post should be recognised as unchanged")
	}
	// Progress is a real change: status is what the run is graded on, so a
	// dedup that swallowed it would recreate the bug the plan tool exists to fix.
	moved := []messages.PlanStep{{Title: "查资料", Status: "done"}, {Title: "写报告", Status: "active"}}
	if p.same(moved) {
		t.Error("a status change must not be treated as unchanged")
	}
	// So is adding a step the agent learned it needs.
	longer := append(append([]messages.PlanStep(nil), steps...), messages.PlanStep{Title: "核对", Status: "pending"})
	if p.same(longer) {
		t.Error("a longer plan must not be treated as unchanged")
	}
	// A nil tracker (no run context, e.g. tests) must report "changed" so the
	// plan still reaches the UI.
	var nilp *planTracker
	if nilp.same(steps) {
		t.Error("a nil tracker must not swallow a plan update")
	}
}

// The step ceiling was the ceiling on how complex a task could be: completion is
// flat at 67-71% up to 30 tool calls and collapses to 18% past it, and 30 calls
// is 15 cycles at the measured 2.1 calls per cycle. The runs that did finish
// real work spent 43 and 94 calls.
func TestStepBudgetAllowsRealWork(t *testing.T) {
	if einoMaxIters < 40 {
		t.Fatalf("einoMaxIters = %d; a 15-cycle budget capped tasks at ~31 tool calls", einoMaxIters)
	}
	// The guard leaves the final cycle tool-free so the model can still answer,
	// so it trips one short of the constant.
	b := newRunBudget(einoMaxIters, 0, 0)
	var msgs []*schema.Message
	for i := 0; i < einoMaxIters-2; i++ {
		msgs = append(msgs, schema.AssistantMessage("step", nil))
		if b.observe(msgs) {
			t.Fatalf("budget tripped at cycle %d of %d", i+1, einoMaxIters)
		}
	}
	msgs = append(msgs, schema.AssistantMessage("step", nil))
	if !b.observe(msgs) {
		t.Fatal("budget did not trip at its final cycle")
	}
	if b.exhausted() != "steps" {
		t.Errorf("exhausted reason = %q, want steps", b.exhausted())
	}
}

// eino's own MaxIterations is a hard error cliff; the guard is meant to reach
// its tool-stripping cycle FIRST so the run reports honestly instead of
// erroring. They are built from the same constant, and drifting apart breaks
// one side or the other silently.
func TestStepBudgetTripsBeforeEinoCliff(t *testing.T) {
	b := newRunBudget(einoMaxIters, 0, 0)
	var msgs []*schema.Message
	for i := 0; i < einoMaxIters; i++ {
		msgs = append(msgs, schema.AssistantMessage("step", nil))
		if b.observe(msgs) {
			if got := i + 1; got >= einoMaxIters {
				t.Fatalf("guard tripped at cycle %d, at or past eino's cliff of %d", got, einoMaxIters)
			}
			return
		}
	}
	t.Fatalf("guard never tripped within %d cycles", einoMaxIters)
}
