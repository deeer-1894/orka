//go:build !linux

package runner

import (
	"context"
	"fmt"
	"os"
)

func (cfg Config) runSandbox(_ context.Context, _ Request, _ string, _ *os.File, _ []string) (Outcome, error) {
	return Outcome{ExitCode: -1}, fmt.Errorf("%w: Linux bubblewrap required; execution refused", ErrSandboxUnavailable)
}
