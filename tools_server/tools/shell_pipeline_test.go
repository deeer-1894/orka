package tools

import (
	"strings"
	"testing"
)

func TestShellDoesNotMaskFailedVerification(t *testing.T) {
	t.Setenv("CODE_SANDBOX_MODE", "unsafe-dev")
	for _, command := range []string{
		`sh -c 'printf failed; exit 7' | tee test.log; echo EXIT=$?`,
		`sh -c 'exit 7'; printf false-success`,
	} {
		r, err := shellExec(t.TempDir())(writeGuardContext(), callWith(map[string]any{"command": command}))
		if err != nil {
			t.Fatal(err)
		}
		out := decodeOutcome(t, r)
		if out.OK || out.ExitCode != 7 || strings.Contains(out.Stdout, "EXIT=0") || strings.Contains(out.Stdout, "false-success") {
			t.Fatalf("failure masked: %+v", out)
		}
	}
}

func TestShellAllowsExplicitExpectedFailure(t *testing.T) {
	t.Setenv("CODE_SANDBOX_MODE", "unsafe-dev")
	r, _ := shellExec(t.TempDir())(writeGuardContext(), callWith(map[string]any{"command": `if sh -c 'exit 7'; then exit 1; else printf handled; fi; printf ' FAILED is just text'`}))
	if out := decodeOutcome(t, r); !out.OK || !strings.Contains(out.Stdout, "handled") {
		t.Fatalf("%+v", out)
	}
}
