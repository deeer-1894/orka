//go:build unix

package runner

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		groupErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		directErr := cmd.Process.Kill() // also kill a direct process that changed groups
		if groupErr == nil || directErr == nil {
			return nil
		}
		if errors.Is(groupErr, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return groupErr
	}
}
