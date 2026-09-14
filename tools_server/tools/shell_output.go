package tools

import (
	"context"
	"io"
	"os"
	"os/exec"
	"time"
)

// Own the capture pipe instead of relying on Cmd's copy goroutines: descendants
// can outlive the shell, and Cmd.Wait may stop watching cancellation once the
// shell exits. Valid descendant output gets the remaining command deadline.
func shellOutput(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	defer writer.Close()
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	_ = writer.Close()
	output := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(reader); output <- data }()
	err = cmd.Wait()
	if err != nil || ctx.Err() != nil {
		_ = cmd.Cancel()
	}
	select {
	case data := <-output:
		return data, err
	case <-ctx.Done():
		_ = cmd.Cancel()
		// Allow partial output to drain after cancellation. Even descendants that
		// escape the process group cannot keep this tool blocked on an open pipe.
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case data := <-output:
			return data, err
		case <-timer.C:
			_ = reader.Close()
			return <-output, err
		}
	}
}
