package tools

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/tools_server/identity"
)

// shellDenylist blocks the most catastrophic commands as defense-in-depth. This
// is NOT a sandbox — the command still runs as the gateway's OS user — but it
// stops the worst accidental/hallucinated damage (host-wide deletes, privilege
// escalation, remote-code-exec pipes, key theft, machine control). For real
// isolation, run the tools gateway inside a container/VM (see shellExec docs).
var shellDenylist = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-[a-zA-Z]*\s*(/|~|\$HOME|/\*|\.\.)`), // rm -rf targeting / ~ .. etc.
	regexp.MustCompile(`(?i)\b(sudo|doas)\b`),                          // privilege escalation
	regexp.MustCompile(`(?i)\b(shutdown|reboot|halt|poweroff|init\s+0)\b`),
	regexp.MustCompile(`(?i)\bmkfs|\bdd\s+if=|\bfdisk\b`),                 // disk wipe
	regexp.MustCompile(`:\s*\(\s*\)\s*\{.*\}\s*;`),                        // fork bomb
	regexp.MustCompile(`(?i)\b(curl|wget|fetch)\b[^|]*\|\s*(sudo\s+)?(sh|bash|zsh|python3?)`), // pipe-to-shell RCE
	regexp.MustCompile(`(?i)(id_rsa|id_ed25519|\.ssh/|\.aws/credentials|\.config/gcloud)`),    // credential theft
}

// unsafeShell returns a non-empty reason if the command matches the denylist.
func unsafeShell(cmd string) string {
	for _, re := range shellDenylist {
		if re.MatchString(cmd) {
			return "command matches a blocked dangerous pattern (" + re.String() + ")"
		}
	}
	return ""
}

// shellExec runs a shell command confined to the caller's workspace directory.
// This is the Manus-style "computer" capability: beyond the browser, the agent
// gets a real terminal — run CLI tools, scripts, git, package managers, data
// processing, or code it just wrote (e.g. `python3 script.py`).
//
// It is intentionally powerful, so it is fenced:
//   - cwd is locked to the per-user workspace root (base/<email>); HOME points there too
//   - a hard timeout caps runaway commands (default 30s, max 120s)
//   - combined stdout+stderr is size-capped
//   - a non-zero exit or timeout is returned as a tool OBSERVATION (not a fatal
//     error), so the agent can read the failure and adapt
//   - it is registered ONLY when SHELL_TOOL=1; for untrusted workloads run the
//     gateway inside a container/VM, which is the real isolation boundary
//     (exactly how Manus sandboxes its shell).
func shellExec(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		command := strings.TrimSpace(req.GetString("command", ""))
		if command == "" {
			return mcp.NewToolResultError("command is required"), nil
		}
		if reason := unsafeShell(command); reason != "" {
			return mcp.NewToolResultText("refused for safety: " + reason +
				". The shell is confined to your workspace and blocks host-destructive commands. " +
				"If you genuinely need this, run it yourself in a sandbox."), nil
		}

		root := pathsafe.UserRoot(base, identity.From(ctx).Email)
		if err := os.MkdirAll(root, 0o755); err != nil {
			return mcp.NewToolResultError("workspace unavailable: " + err.Error()), nil
		}

		timeout := time.Duration(req.GetInt("timeout_sec", 30)) * time.Second
		if timeout <= 0 || timeout > 120*time.Second {
			timeout = 30 * time.Second
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		c := exec.CommandContext(cctx, "sh", "-c", command)
		c.Dir = root
		// Confine writes/config to the workspace by pointing HOME there; keep the
		// inherited PATH so common tools (git, python3, node, …) resolve.
		c.Env = append(os.Environ(), "HOME="+root)

		out, err := c.CombinedOutput()
		text := string(out)
		const maxOut = 16 * 1024
		if len(text) > maxOut {
			text = text[:maxOut] + "\n…(output truncated at 16KB)"
		}

		switch {
		case cctx.Err() == context.DeadlineExceeded:
			return mcp.NewToolResultText("command timed out after " + timeout.String() + "; partial output:\n" + text), nil
		case err != nil:
			return mcp.NewToolResultText("command exited with error: " + err.Error() + "\n--- output ---\n" + text), nil
		case strings.TrimSpace(text) == "":
			return mcp.NewToolResultText("(command succeeded, exit 0, no output)"), nil
		default:
			return mcp.NewToolResultText(text), nil
		}
	}
}
