//go:build !unix

package tools

import "os/exec"

// Non-Unix platforms retain CommandContext's process cancellation. WaitDelay
// still bounds inherited pipe reads; process-tree cleanup requires OS support.
func configureShellProcess(cmd *exec.Cmd) {}
