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

// The adapter must not silently reinstate Eino's default 20-cycle limit.
func TestUnlimitedIterationAdapterAndMeter(t *testing.T) {
	if einoMaxIters != int(^uint(0)>>1) {
		t.Fatal("unexpected iteration quota")
	}
	b := newRunBudget(0, 0, 0)
	var msgs []*schema.Message
	for i := 0; i < 1000; i++ {
		msgs = append(msgs, schema.AssistantMessage("step", nil))
		if b.observe(msgs) {
			t.Fatalf("usage meter stopped cycle %d", i)
		}
	}
}
