package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"
)

// TestWorkspaceBackendOffload verifies that oversized tool output offloaded by
// the reduction middleware lands in the caller's OWN workspace, so the agent can
// retrieve it with the file_read tool Orka already exposes (an in-memory backend
// would point the model at a file none of Orka's tools can open).
func TestWorkspaceBackendOffload(t *testing.T) {
	base := t.TempDir()
	email := "ctx@test.com"
	be := newWorkspaceBackend(base, email)
	if be == nil {
		t.Fatal("expected a backend when storage is configured")
	}
	ctx := context.Background()
	body := strings.Repeat("report line\n", 500)

	if err := be.Write(ctx, &filesystem.WriteRequest{FilePath: "call-123", Content: body}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// It must be a real file inside the user's workspace, under the offload dir.
	onDisk := filepath.Join(base, email, offloadDir, "call-123")
	if _, err := os.Stat(onDisk); err != nil {
		t.Fatalf("offloaded content is not in the user's workspace: %v", err)
	}

	got, err := be.Read(ctx, &filesystem.ReadRequest{FilePath: filepath.Join(offloadDir, "call-123")})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(got.Content, "report line") {
		t.Fatalf("read back wrong content: %.40q", got.Content)
	}

	// Line windowing (how an agent pages through a big offloaded file).
	win, err := be.Read(ctx, &filesystem.ReadRequest{FilePath: filepath.Join(offloadDir, "call-123"), Offset: 2, Limit: 3})
	if err != nil {
		t.Fatalf("windowed read: %v", err)
	}
	if n := len(strings.Split(win.Content, "\n")); n != 3 {
		t.Fatalf("windowed read returned %d lines, want 3", n)
	}
}

// TestWorkspaceBackendConfinement: a traversal path must not escape the caller's
// storage root — offload paths are derived from model-supplied tool call ids.
func TestWorkspaceBackendConfinement(t *testing.T) {
	base := t.TempDir()
	be := &workspaceBackend{root: filepath.Join(base, "victim")}
	if _, err := be.Read(context.Background(), &filesystem.ReadRequest{FilePath: "../../etc/passwd"}); err == nil {
		t.Fatal("traversal outside the workspace root must be rejected")
	}
}

// TestContextHandlersBuild asserts the P1 chain actually constructs (a silent
// build failure would leave runs with no context management at all).
func TestContextHandlersBuild(t *testing.T) {
	mw := contextHandlers(context.Background(), t.TempDir(), "ctx@test.com")
	if len(mw) < 2 {
		t.Fatalf("expected patchtoolcalls + reduction handlers, got %d", len(mw))
	}
}

// A sub-agent's result is the most expensive output in the system — a whole
// nested agent run. Clearing it as "an old tool result" cost the worst run
// measured here: four researchers returned sourced reports in 140 seconds, the
// reducer replaced them with placeholders, and the orchestrator spent 680
// seconds re-reading unrelated files with nothing left to synthesise.
func TestSubAgentResultsAreProtectedFromReduction(t *testing.T) {
	protected := map[string]bool{}
	for _, n := range append(protectedToolOutputs(), subAgentNames()...) {
		protected[n] = true
	}
	for _, sp := range DefaultSubAgents() {
		if sp.Name != "" && !protected[sp.Name] {
			t.Errorf("sub-agent %q may be cleared from context", sp.Name)
		}
	}
	// The pipeline's own control tools must stay protected too.
	for _, n := range []string{"validate_factor", "factor_agreement", planToolName} {
		if !protected[n] {
			t.Errorf("%q lost its protection", n)
		}
	}
}
