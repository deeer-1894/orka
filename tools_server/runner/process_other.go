//go:build !unix

package runner

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}
