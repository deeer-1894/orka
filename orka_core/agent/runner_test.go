package agent

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_core/messages"
)

// recMW records its name when handled and optionally interrupts on the first
// (pre-resume) pass.
type recMW struct {
	name      string
	order     *[]string
	interrupt bool
}

func (m recMW) Name() string { return m.name }

func (m recMW) Handle(rc *RunContext, next func(*RunContext) error) error {
	*m.order = append(*m.order, m.name)
	if m.interrupt {
		if _, resumed := rc.Vars["resume_key"]; !resumed {
			rc.Interrupt = &Interrupt{
				Reason:  "clarify",
				Clarify: &messages.ClarifyMessage{Question: "which?", ResumeKey: "cp1"},
			}
		}
	}
	return nil
}

func TestRunner_SequentialNoInterrupt(t *testing.T) {
	order := []string{}
	r := NewDefaultRunner(
		recMW{"A", &order, false},
		recMW{"B", &order, false},
		recMW{"C", &order, false},
	)
	rc := &RunContext{Ctx: context.Background()}
	if err := r.Run(rc); err != nil {
		t.Fatal(err)
	}
	if got := join(order); got != "A,B,C" {
		t.Fatalf("order = %s", got)
	}
	if rc.Cursor != 3 {
		t.Fatalf("cursor = %d, want 3", rc.Cursor)
	}
}

func TestRunner_InterruptThenResume(t *testing.T) {
	order := []string{}
	r := NewDefaultRunner(
		recMW{"A", &order, false},
		recMW{"B", &order, true}, // interrupts on first pass
		recMW{"C", &order, false},
	)
	rc := &RunContext{Ctx: context.Background()}

	// Run stops at B.
	if err := r.Run(rc); err != nil {
		t.Fatal(err)
	}
	if got := join(order); got != "A,B" {
		t.Fatalf("after run order = %s, want A,B", got)
	}
	if rc.Interrupt == nil {
		t.Fatal("expected interrupt to be set")
	}
	if rc.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (pointing at B)", rc.Cursor)
	}

	// Resume restarts at B (now with resume_key present) and continues to C.
	if err := r.ResumeWithParams(rc, "cp1", "use option 2"); err != nil {
		t.Fatal(err)
	}
	if got := join(order); got != "A,B,B,C" {
		t.Fatalf("after resume order = %s, want A,B,B,C", got)
	}
	if rc.Interrupt != nil {
		t.Fatal("interrupt should be cleared after resume")
	}
	if rc.Cursor != 3 {
		t.Fatalf("cursor = %d, want 3", rc.Cursor)
	}
	// Resume injected the user reply as a message.
	if n := len(rc.Messages); n == 0 || rc.Messages[n-1].Content != "use option 2" {
		t.Fatalf("user reply not injected: %+v", rc.Messages)
	}
}

func TestRunner_HonorsContextCancel(t *testing.T) {
	order := []string{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	r := NewDefaultRunner(recMW{"A", &order, false})
	rc := &RunContext{Ctx: ctx}
	if err := r.Run(rc); err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if len(order) != 0 {
		t.Fatalf("middleware ran despite cancel: %v", order)
	}
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
