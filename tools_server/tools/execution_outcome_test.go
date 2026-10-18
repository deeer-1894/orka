package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

type observedOutcome struct {
	OK       bool   `json:"ok"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timed_out"`
	Canceled bool   `json:"canceled"`
}

func decodeOutcome(t *testing.T, r *mcp.CallToolResult) observedOutcome {
	t.Helper()
	var out observedOutcome
	if err := json.Unmarshal([]byte(writeGuardText(r)), &out); err != nil {
		t.Fatalf("not outcome JSON: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(writeGuardText(r)), &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ok", "exit_code", "stdout", "stderr", "timed_out", "canceled"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("missing %s", key)
		}
	}
	if r.IsError == out.OK {
		t.Fatalf("MCP and outcome disagree: %+v", out)
	}
	return out
}
func TestExecutionOutcomeContract(t *testing.T) {
	// Use the actual namespace boundary when available; the strict integration
	// job supplies CODE_BWRAP_PATH. Unit tests opt explicitly into local mode.
	if os.Getenv("CODE_BWRAP_PATH") != "" {
		t.Setenv("CODE_SANDBOX_MODE", "bwrap")
	} else {
		t.Setenv("CODE_SANDBOX_MODE", "unsafe-dev")
	}
	for _, name := range []string{"shell", "python"} {
		t.Run(name, func(t *testing.T) {
			for _, state := range []string{"success", "exit", "timeout", "canceled", "refused", "invalid"} {
				t.Run(state, func(t *testing.T) {
					handler := shellExec(t.TempDir())
					args := map[string]any{"command": "printf out; printf err >&2"}
					if name == "python" {
						handler = pythonRun(t.TempDir())
						args = map[string]any{"code": "import sys; print('out', end='', flush=True); print('err', end='', file=sys.stderr, flush=True)"}
					}
					if state == "exit" {
						if name == "shell" {
							args["command"] = args["command"].(string) + "; exit 7"
						} else {
							args["code"] = args["code"].(string) + "; sys.exit(7)"
						}
					}
					ctx := writeGuardContext()
					var cancel context.CancelFunc = func() {}
					if state == "timeout" {
						ctx, cancel = context.WithTimeout(ctx, 150*time.Millisecond)
					}
					if state == "canceled" {
						ctx, cancel = context.WithCancel(ctx)
						time.AfterFunc(150*time.Millisecond, cancel)
					}
					defer cancel()
					if state == "timeout" || state == "canceled" {
						if name == "shell" {
							args["command"] = args["command"].(string) + "; sleep 20"
						} else {
							args["code"] = args["code"].(string) + "; import time; time.sleep(20)"
						}
					}
					if state == "refused" {
						t.Setenv("CODE_SANDBOX_MODE", "invalid-mode")
					}
					if state == "invalid" {
						args = map[string]any{}
					}
					r, err := handler(ctx, callWith(args))
					if err != nil {
						t.Fatal(err)
					}
					if state != "success" && !r.IsError {
						t.Fatal("failed execution reported as MCP success")
					}
					out := decodeOutcome(t, r)
					if state == "success" || state == "exit" {
						if out.Stdout != "out" || out.Stderr != "err" {
							t.Fatalf("streams not preserved: %+v", out)
						}
					}
					if state == "success" && (!out.OK || out.ExitCode != 0) {
						t.Fatalf("success: %+v", out)
					}
					if state == "exit" && (out.OK || out.ExitCode != 7) {
						t.Fatalf("exit: %+v", out)
					}
					if (state == "timeout") != out.TimedOut || (state == "canceled") != out.Canceled {
						t.Fatalf("wrong termination flags: %+v", out)
					}
				})
			}
		})
	}
}
