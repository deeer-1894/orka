package runner

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCleanEnvironmentRejectsOverrides(t *testing.T) {
	for _, entry := range []string{"PATH=/tmp", "LD_PRELOAD=fixture", "OPENAI_API_KEY=fixture", "HOME=/tmp", "PYTHONPATH=/tmp", "SQL_QUERY=x\x00bad"} {
		_, err := (Config{Mode: "unsafe-dev"}).Run(context.Background(), Request{Root: t.TempDir(), Program: "true", Env: []string{entry}})
		if err == nil {
			t.Fatalf("unsafe env override accepted: %q", strings.Split(entry, "=")[0])
		}
	}
}

func TestExplicitDevModeStillUsesCleanEnvironmentAndLimit(t *testing.T) {
	t.Setenv("ORKA_TEST_FAKE_SECRET", "fixture-only")
	out, err := (Config{Mode: "unsafe-dev"}).Run(context.Background(), Request{Root: t.TempDir(), Program: "python3", Args: []string{"-c", `import os
assert 'ORKA_TEST_FAKE_SECRET' not in os.environ
print('x'*1000000)`}, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > OutputLimit+100 || !strings.Contains(out, "truncated") {
		t.Fatalf("capture not bounded: %d", len(out))
	}
}
