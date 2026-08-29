package service

import "testing"

// TestGrantsSurviveRestart: a "本会话始终允许" decision must outlive a control
// plane restart — the old in-memory-only allowlist silently re-prompted the user
// for something they had already approved.
func TestGrantsSurviveRestart(t *testing.T) {
	base := t.TempDir()
	h1 := newConfirmHub()
	h1.store = base
	ch := h1.register("c1", "conv-1", "shell")
	go func() { <-ch }()
	if !h1.resolve("c1", true, true) {
		t.Fatal("resolve failed")
	}
	if !h1.isAllowed("conv-1", "shell") {
		t.Fatal("grant not recorded in memory")
	}

	// A fresh hub (i.e. a restarted process) must still honour the grant.
	h2 := newConfirmHub()
	h2.store = base
	h2.load()
	if !h2.isAllowed("conv-1", "shell") {
		t.Fatal("grant did not survive the restart")
	}
	if h2.isAllowed("conv-1", "python") || h2.isAllowed("other", "shell") {
		t.Fatal("grant leaked to another tool/conversation")
	}
}
