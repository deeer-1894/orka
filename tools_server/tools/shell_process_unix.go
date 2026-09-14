//go:build unix

package tools

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Put each tool command in its own process group. Killing only the shell leaves
// descendants running and holding CombinedOutput pipes open after the deadline.
func configureShellProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
