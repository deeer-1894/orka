package runner

import (
	"context"
	"io"
	"os"
	"os/exec"
	"time"
)

type boundedOutput struct {
	data      []byte
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := OutputLimit - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return n, nil
}
func (b *boundedOutput) String() string {
	if b.truncated {
		return string(b.data) + "\n…(output truncated at 32KB)"
	}
	return string(b.data)
}

func capture(ctx context.Context, cmd *exec.Cmd) (Outcome, error) {
	configureProcess(cmd)
	cmd.WaitDelay = 250 * time.Millisecond
	out := Outcome{ExitCode: -1}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return out, err
	}
	defer stdout.Close()
	defer stdoutWriter.Close()
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		return out, err
	}
	defer stderr.Close()
	defer stderrWriter.Close()
	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	if err := cmd.Start(); err != nil {
		return out, err
	}
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	type stream struct {
		stderr bool
		text   string
	}
	output := make(chan stream, 2)
	drain := func(r *os.File, isError bool) {
		var b boundedOutput
		_, _ = io.Copy(&b, r)
		output <- stream{isError, b.String()}
	}
	go drain(stdout, false)
	go drain(stderr, true)
	err = cmd.Wait()
	if cmd.ProcessState != nil {
		out.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil || ctx.Err() != nil {
		_ = cmd.Cancel()
	}
	collect := func(s stream) {
		if s.stderr {
			out.Stderr = s.text
		} else {
			out.Stdout = s.text
		}
	}
	for remaining := 2; remaining > 0; remaining-- {
		select {
		case s := <-output:
			collect(s)
		case <-ctx.Done():
			_ = cmd.Cancel()
			// Descendants may retain descriptors after their parent exits. Bound the
			// final drain even if they escaped the process group in unsafe-dev mode.
			timer := time.NewTimer(250 * time.Millisecond)
			for remaining > 0 {
				select {
				case s := <-output:
					collect(s)
					remaining--
				case <-timer.C:
					_ = stdout.Close()
					_ = stderr.Close()
				}
			}
			timer.Stop()
			return out, ctx.Err()
		}
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return out, err
}
