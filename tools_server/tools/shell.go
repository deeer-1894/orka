package tools

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/tools_server/identity"
	"github.com/orka-oss/tools_server/runner"
)

// shellDenylist catches common destructive mistakes before execution. The
// runner's namespace and mount boundary enforces isolation independently.
var shellDenylist = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-[a-zA-Z]*\s*(/|~|\$HOME|/\*|\.\.)`), // rm -rf targeting / ~ .. etc.
	regexp.MustCompile(`(?i)\b(sudo|doas)\b`),                           // privilege escalation
	regexp.MustCompile(`(?i)\b(shutdown|reboot|halt|poweroff|init\s+0)\b`),
	regexp.MustCompile(`(?i)\bmkfs|\bdd\s+if=|\bfdisk\b`),                                     // disk wipe
	regexp.MustCompile(`:\s*\(\s*\)\s*\{.*\}\s*;`),                                            // fork bomb
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

// shellExec delegates all process execution to runner. A working directory is
// not a sandbox: production requires the bwrap boundary, with only this session
// mounted at /workspace. CODE_SANDBOX_MODE=unsafe-dev is an explicit opt-out.
func shellExec(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		command := strings.TrimSpace(req.GetString("command", ""))
		if command == "" {
			return executionFailure(fmt.Errorf("command is required")), nil
		}
		if reason := unsafeShell(command); reason != "" {
			return executionFailure(fmt.Errorf("command refused: %s", reason)), nil
		}

		root, rootErr := pathsafe.EnsureSession(base, identity.From(ctx).Email, identity.From(ctx).ConversationID)
		if rootErr != nil {
			return executionFailure(rootErr), nil
		}
		if err := os.MkdirAll(root, pathsafe.WorkspaceDirMode); err != nil {
			return executionFailure(fmt.Errorf("workspace unavailable: %w", err)), nil
		}

		timeout := time.Duration(req.GetInt("timeout_sec", 30)) * time.Second
		if timeout <= 0 || timeout > 120*time.Second {
			timeout = 30 * time.Second
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		out, _ := runner.FromEnv().Execute(cctx, runner.Request{Root: root, Program: "bash", Args: []string{"--noprofile", "--norc", "-e", "-o", "pipefail", "-c", command}, TrackFiles: true, Timeout: timeout})
		return executionResult(out), nil
	}
}
