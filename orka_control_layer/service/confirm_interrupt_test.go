package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// TestCheckpointStoreRoundTrip: a paused run's state must land on disk and come
// back — that is what lets an approval outlive a control-plane restart.
func TestCheckpointStoreRoundTrip(t *testing.T) {
	base := t.TempDir()
	st := newCheckpointStore(base)
	if st == nil {
		t.Fatal("expected a store when storage is configured")
	}
	ctx := context.Background()
	if _, found, err := st.Get(ctx, "conv-1"); err != nil || found {
		t.Fatalf("empty store should report not-found: found=%v err=%v", found, err)
	}
	if err := st.Set(ctx, "conv-1", []byte("state")); err != nil {
		t.Fatalf("set: %v", err)
	}
	b, found, err := st.Get(ctx, "conv-1")
	if err != nil || !found || string(b) != "state" {
		t.Fatalf("get: %q found=%v err=%v", b, found, err)
	}
	// A fresh store over the same dir (i.e. after a restart) still sees it.
	if b2, found2, _ := newCheckpointStore(base).Get(ctx, "conv-1"); !found2 || string(b2) != "state" {
		t.Fatal("checkpoint did not survive a store rebuild")
	}
	// Ids never escape the directory.
	_ = st.Set(ctx, "../../escape", []byte("x"))
	if _, err := os.Stat(filepath.Join(base, "escape.ckpt")); err == nil {
		t.Fatal("checkpoint id escaped the store directory")
	}
}

// TestPausedRunRoundTrip: the descriptor needed to REBUILD an interrupted run
// must persist, otherwise a restart cannot resume it.
func TestPausedRunRoundTrip(t *testing.T) {
	base := t.TempDir()
	if _, ok := loadPausedRun(base, "conv-x"); ok {
		t.Fatal("nothing should be pending yet")
	}
	savePausedRun(base, pausedRun{
		Request: ChatRunRequest{ConversationID: "conv-x", UserEmail: "u@x.com", ConfirmRisky: true},
		Target:  "agent:orka;tool:call_1", Tool: "shell", Summary: "rm -rf /tmp/x",
	})
	got, ok := loadPausedRun(base, "conv-x")
	if !ok || got.Target != "agent:orka;tool:call_1" || got.Tool != "shell" {
		t.Fatalf("paused run did not round-trip: %+v ok=%v", got, ok)
	}
	if got.Request.UserEmail != "u@x.com" || !got.Request.ConfirmRisky {
		t.Fatalf("request not preserved for rebuild: %+v", got.Request)
	}
	dropPausedRun(base, "conv-x")
	if _, ok := loadPausedRun(base, "conv-x"); ok {
		t.Fatal("dropped run should no longer be pending")
	}
}

// TestGateInterruptsInsteadOfBlocking: with the interrupt path on, a danger tool
// must return an interrupt error immediately rather than parking a goroutine for
// the five-minute confirm timeout.
func TestGateInterruptsInsteadOfBlocking(t *testing.T) {
	hub := newConfirmHub()
	g := confirmGate{inner: gateStubTool{name: "shell"}, hub: hub, interruptible: true}
	ctx := agent.WithEmit(
		agent.WithMeta(context.Background(), messages.Meta{ConversationID: "c1", UserEmail: "u@x.com"}),
		func(messages.Message) {},
	)
	done := make(chan error, 1)
	go func() { _, err := g.Invoke(ctx, map[string]any{"command": "ls"}); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an interrupt error, got nil (the tool ran without approval)")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gate blocked instead of interrupting (the 5-minute park is back)")
	}
}

// TestGateHonoursSessionGrant: once "always allow" is granted, the tool runs
// straight through — no interrupt, no prompt.
func TestGateHonoursSessionGrant(t *testing.T) {
	base := t.TempDir()
	hub := newConfirmHub()
	hub.store = base
	hub.grant("c1", "shell")

	g := confirmGate{inner: gateStubTool{name: "shell"}, hub: hub, interruptible: true}
	ctx := agent.WithEmit(
		agent.WithMeta(context.Background(), messages.Meta{ConversationID: "c1"}),
		func(messages.Message) {},
	)
	out, err := g.Invoke(ctx, map[string]any{"command": "ls"})
	if err != nil || out != "ran:shell" {
		t.Fatalf("granted tool should run: out=%q err=%v", out, err)
	}
	// ...and only for that conversation.
	ctx2 := agent.WithEmit(agent.WithMeta(context.Background(), messages.Meta{ConversationID: "c2"}), func(messages.Message) {})
	if _, err := g.Invoke(ctx2, map[string]any{"command": "ls"}); err == nil {
		t.Fatal("grant leaked to another conversation")
	}
}

type gateStubTool struct{ name string }

func (e gateStubTool) Name() string           { return e.name }
func (e gateStubTool) Description() string    { return "test tool" }
func (e gateStubTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (e gateStubTool) Invoke(context.Context, map[string]any) (string, error) {
	return "ran:" + e.name, nil
}

// TestInterruptErrIsNotSwallowed guards a real safety bug: the tool adapter used
// to convert EVERY tool error into a "recoverable" observation, including the
// approval interrupt — so the model happily retried an unapproved danger tool
// three times. The detector must recognise what compose.Interrupt returns.
func TestInterruptErrIsNotSwallowed(t *testing.T) {
	hub := newConfirmHub()
	g := confirmGate{inner: gateStubTool{name: "shell"}, hub: hub, interruptible: true}
	ctx := agent.WithEmit(agent.WithMeta(context.Background(), messages.Meta{ConversationID: "c1"}), func(messages.Message) {})

	_, err := g.Invoke(ctx, map[string]any{"command": "ls"})
	if err == nil {
		t.Fatal("gate returned no error — the danger tool ran unapproved")
	}
	if !isInterruptErr(err) {
		t.Fatalf("interrupt not recognised, so the adapter would swallow it: %T %v", err, err)
	}
	// A plain tool failure must NOT look like an interrupt.
	if isInterruptErr(errors.New("upstream 502")) {
		t.Fatal("a normal tool error was misclassified as an interrupt")
	}
}
