package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orka-oss/tools_server/identity"
)

func TestExecutionRejectsUnknownSandboxMode(t *testing.T) {
	t.Setenv("CODE_EXECUTION", "1")
	t.Setenv("CODE_SANDBOX_MODE", "typo")
	for _, name := range []string{"shell", "python"} {
		handler := shellExec(t.TempDir())
		args := map[string]any{"command": "printf must-not-run"}
		if name == "python" {
			handler = pythonRun(t.TempDir())
			args = map[string]any{"code": "print('must-not-run')"}
		}
		r, err := handler(writeGuardContext(), callWith(args))
		if err != nil {
			t.Fatal(err)
		}
		if !r.IsError || strings.Contains(writeGuardText(r), "must-not-run") {
			t.Fatalf("%s executed with invalid sandbox mode", name)
		}
	}
}

func TestExecutionEnvironmentDoesNotInheritGateway(t *testing.T) {
	t.Setenv("CODE_EXECUTION", "1")
	t.Setenv("CODE_SANDBOX_MODE", "unsafe-dev")
	t.Setenv("ORKA_TEST_FAKE_SECRET", "fixture-only")
	r, err := shellExec(t.TempDir())(writeGuardContext(), callWith(map[string]any{"command": `if [ -n "${ORKA_TEST_FAKE_SECRET+x}" ]; then printf inherited; else printf clean; fi`}))
	if err != nil || r.IsError || decodeOutcome(t, r).Stdout != "clean" {
		t.Fatalf("clean environment contract failed: %v %v", err, r)
	}
}

func TestExecutionCrossSessionIsolationOrExplicitRefusal(t *testing.T) {
	t.Setenv("CODE_EXECUTION", "1")
	t.Setenv("CODE_SANDBOX_MODE", "bwrap")
	base := t.TempDir()
	own := filepath.Join(base, "fake-a@example.test", "sessions", "one")
	other := filepath.Join(base, "fake-b@example.test", "sessions", "two", "private.txt")
	if err := os.MkdirAll(own, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(other), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("synthetic-cross-session-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := identity.With(context.Background(), identity.Identity{Email: "fake-a@example.test", ConversationID: "one"})
	command := fmt.Sprintf(`if [ -r %q ]; then printf cross-session-visible; else printf isolated; fi`, other)
	r, err := shellExec(base)(ctx, callWith(map[string]any{"command": command}))
	if err != nil {
		t.Fatal(err)
	}
	result := writeGuardText(r)
	if r.IsError && strings.Contains(result, "sandbox unavailable") {
		t.Log("sandbox unavailable: execution explicitly refused")
		return
	}
	if r.IsError || decodeOutcome(t, r).Stdout != "isolated" {
		t.Fatalf("session boundary failed: %s", result)
	}
}

func TestOfficeRunnerOutputIsBounded(t *testing.T) {
	t.Setenv("CODE_SANDBOX_MODE", "unsafe-dev")
	out, err := runInWorkspace(context.Background(), t.TempDir(), "python3", []string{"-c", "print('x'*200000)"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 33000 || !strings.Contains(out, "truncated") {
		t.Fatalf("runner kept unbounded output: %d bytes", len(out))
	}
}
