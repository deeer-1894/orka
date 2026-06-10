package middlewares

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

func TestInterruptMid_SetsInterruptFromPendingClarify(t *testing.T) {
	rc := &agent.RunContext{
		Ctx:  context.Background(),
		Vars: map[string]any{VarPendingClarify: messages.ClarifyMessage{Question: "which?"}},
	}
	if err := (&Interrupt{}).Handle(rc, nil); err != nil {
		t.Fatal(err)
	}
	if rc.Interrupt == nil || rc.Interrupt.Clarify == nil || rc.Interrupt.Clarify.Question != "which?" {
		t.Fatalf("interrupt not set from pending clarify: %+v", rc.Interrupt)
	}
}

func TestInterruptMid_NoopWhenResumed(t *testing.T) {
	rc := &agent.RunContext{
		Ctx: context.Background(),
		Vars: map[string]any{
			VarPendingClarify: messages.ClarifyMessage{Question: "which?"},
			VarResumeKey:      "cp1",
		},
	}
	if err := (&Interrupt{}).Handle(rc, nil); err != nil {
		t.Fatal(err)
	}
	if rc.Interrupt != nil {
		t.Fatal("should not interrupt during resume")
	}
}

func TestInterruptMid_NoopWhenNothingPending(t *testing.T) {
	rc := &agent.RunContext{Ctx: context.Background(), Vars: map[string]any{}}
	if err := (&Interrupt{}).Handle(rc, nil); err != nil {
		t.Fatal(err)
	}
	if rc.Interrupt != nil {
		t.Fatal("should not interrupt with nothing pending")
	}
}
