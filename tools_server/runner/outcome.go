package runner

import (
	"context"
	"errors"
)

// Outcome describes process execution independently of MCP. ExitCode is -1 if
// the process did not start or terminated by signal. Error explains refusal or
// termination without mixing runner diagnostics into the process's stderr.
type Outcome struct {
	FileChanges *FileChanges `json:"file_changes,omitempty"`
	OK          bool         `json:"ok"`
	ExitCode    int          `json:"exit_code"`
	Stdout      string       `json:"stdout"`
	Stderr      string       `json:"stderr"`
	TimedOut    bool         `json:"timed_out"`
	Canceled    bool         `json:"canceled"`
	Error       string       `json:"error,omitempty"`
}

// Execute returns machine-readable status and a Go error for failed/refused
// execution. Both output streams are bounded independently at capture time.
func (cfg Config) Execute(ctx context.Context, req Request) (Outcome, error) {
	out, err := cfg.execute(ctx, req)
	out.OK = err == nil
	out.TimedOut = errors.Is(err, context.DeadlineExceeded)
	out.Canceled = errors.Is(err, context.Canceled)
	if err != nil {
		out.Error = err.Error()
	}
	return out, err
}

// Run preserves the text adapter used by built-in office generators. Separate
// streams are concatenated (stdout first) with a shared total output limit.
func (cfg Config) Run(ctx context.Context, req Request) (string, error) {
	out, err := cfg.Execute(ctx, req)
	var combined boundedOutput
	_, _ = combined.Write([]byte(out.Stdout))
	_, _ = combined.Write([]byte(out.Stderr))
	return combined.String(), err
}
