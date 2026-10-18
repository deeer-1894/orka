package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOutcomeBoundsStreamsIndependently(t *testing.T) {
	out, err := (Config{Mode: "unsafe-dev"}).Execute(context.Background(), Request{Root: t.TempDir(), Program: "python3", Args: []string{"-c", `import sys; sys.stdout.write('o'*200000); sys.stderr.write('e'*200000); sys.exit(9)`}})
	if err == nil || out.OK || out.ExitCode != 9 {
		t.Fatalf("status: %+v %v", out, err)
	}
	if !strings.HasPrefix(out.Stdout, "oooo") || !strings.HasPrefix(out.Stderr, "eeee") {
		t.Fatal("stream attribution lost")
	}
	for _, data := range []string{out.Stdout, out.Stderr} {
		if len(data) > OutputLimit+100 || !strings.Contains(data, "truncated") {
			t.Fatalf("unbounded/missing truncation: %d", len(data))
		}
	}
}
func TestOutcomeInternalTimeoutAndRefusal(t *testing.T) {
	out, err := (Config{Mode: "unsafe-dev"}).Execute(context.Background(), Request{Root: t.TempDir(), Program: "sh", Args: []string{"-c", "sleep 20"}, Timeout: 50 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || out.OK || !out.TimedOut || out.Canceled {
		t.Fatalf("timeout: %+v %v", out, err)
	}
	out, err = (Config{Mode: "invalid"}).Execute(context.Background(), Request{Root: t.TempDir(), Program: "true"})
	if !errors.Is(err, ErrSandboxUnavailable) || out.OK || out.ExitCode != -1 || out.Error == "" {
		t.Fatalf("refusal: %+v %v", out, err)
	}
}
