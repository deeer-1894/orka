//go:build linux

package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/orka-oss/tools_server/identity"
)

func TestShellTimeoutClosesDescendantPipes(t *testing.T) {
	for _, name := range []string{"deadline", "cancellation", "shell_exits_before_child", "failed_shell_exits_before_child", "direct_process_changes_group"} {
		cancelParent := name == "cancellation"

		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			ctx, cancel := context.WithCancel(identity.With(context.Background(), identity.Identity{Email: "tester", ConversationID: "timeout"}))
			defer cancel()
			pidFile := filepath.Join(base, "tester", "sessions", "timeout", "child.pid")
			done := make(chan *mcp.CallToolResult, 1)
			command := `sh -c 'echo $$ > child.pid; exec sleep 30' & wait`
			if name == "shell_exits_before_child" {
				command = `sh -c 'echo $$ > child.pid; exec sleep 30' &`
			}
			if name == "failed_shell_exits_before_child" {
				command = `sh -c 'echo $$ > child.pid; exec sleep 30' & while [ ! -s child.pid ]; do sleep 0.01; done; exit 1`
			}
			if name == "direct_process_changes_group" {
				if _, err := exec.LookPath("python3"); err != nil {
					t.Skip("python3 is unavailable")
				}
				command = `exec python3 -c 'import os,time; os.setpgid(0,os.getpgid(os.getppid())); open("child.pid","w").write(str(os.getpid())); time.sleep(30)'`
			}
			go func() {
				result, _ := shellExec(base)(ctx, callWith(map[string]any{"command": command, "timeout_sec": 1}))
				done <- result
			}()
			deadline := time.Now().Add(2 * time.Second)
			pid := 0
			for time.Now().Before(deadline) {
				b, _ := os.ReadFile(pidFile)
				pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
				if pid > 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid == 0 {
				t.Fatal("child did not start")
			}
			defer syscall.Kill(pid, syscall.SIGKILL)
			if cancelParent {
				cancel()
			}
			select {
			case result := <-done:
				if result == nil {
					t.Fatal("missing timeout observation")
				}
				if name == "deadline" {
					text := ""
					for _, c := range result.Content {
						if v, ok := c.(mcp.TextContent); ok {
							text += v.Text
						}
					}
					if !strings.Contains(text, "timed out") {
						t.Errorf("missing timeout observation: %s", text)
					}
				}
			case <-time.After(3 * time.Second):
				// Unblock the old implementation before failing; leave no test processes.
				syscall.Kill(pid, syscall.SIGKILL)
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				t.Fatal("shell did not return: descendant retained output pipes after cancellation")
			}
			deadline = time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
				fields := strings.Fields(string(b))
				if os.IsNotExist(err) || len(fields) > 2 && fields[2] == "Z" {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("descendant survived shell timeout/cancellation")
		})
	}
}

func TestShellSuccessfulOutputStillReturned(t *testing.T) {
	ctx := identity.With(context.Background(), identity.Identity{Email: "tester", ConversationID: "normal"})
	r, err := shellExec(t.TempDir())(ctx, callWith(map[string]any{"command": "printf ready"}))
	if err != nil || r == nil {
		t.Fatalf("shell: %v", err)
	}
	if len(r.Content) != 1 || r.Content[0].(mcp.TextContent).Text != "ready" {
		t.Fatalf("unexpected output: %+v", r)
	}
}

func TestShellAllowsChildOutputWithinDeadline(t *testing.T) {
	ctx := identity.With(context.Background(), identity.Identity{Email: "tester", ConversationID: "child-output"})
	r, err := shellExec(t.TempDir())(ctx, callWith(map[string]any{"command": "(sleep 1; printf ready) &", "timeout_sec": 3}))
	if err != nil || r == nil {
		t.Fatalf("shell: %v", err)
	}
	if len(r.Content) != 1 || r.Content[0].(mcp.TextContent).Text != "ready" {
		t.Fatalf("lost valid child output: %+v", r)
	}
}
