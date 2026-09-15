//go:build linux

package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Only immutable runtime files are visible alongside this one writable session.
// There is no host /home, storage parent, /run, socket, host /tmp, host /proc,
// or network namespace. The workspace mount source is a pinned descriptor.
func (cfg Config) runSandbox(ctx context.Context, req Request, rootPath string, root *os.File, env []string) (Outcome, error) {
	bwrap := cfg.BwrapPath
	if bwrap == "" {
		bwrap = "/usr/bin/bwrap"
	}
	if !filepath.IsAbs(bwrap) {
		return Outcome{ExitCode: -1}, fmt.Errorf("%w: CODE_BWRAP_PATH must be absolute", ErrSandboxUnavailable)
	}
	st, err := os.Stat(bwrap)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&0111 == 0 {
		return Outcome{ExitCode: -1}, fmt.Errorf("%w: install bubblewrap and permit user/mount/PID/network namespaces; execution refused", ErrSandboxUnavailable)
	}
	args := []string{"--unshare-all", "--unshare-user", "--unshare-pid", "--unshare-net", "--die-with-parent", "--new-session", "--cap-drop", "ALL"}
	mounts := []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc/ld.so.cache", "/etc/alternatives", "/etc/fonts", "/etc/ssl/certs", "/etc/localtime"}
	for _, p := range mounts {
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return Outcome{ExitCode: -1}, fmt.Errorf("%w: runtime unavailable", ErrSandboxUnavailable)
		}
		canonical, err := filepath.EvalSymlinks(p)
		if err != nil {
			return Outcome{ExitCode: -1}, fmt.Errorf("%w: runtime path invalid", ErrSandboxUnavailable)
		}
		// A runtime mount must never also expose a workspace or its parent.
		if pathContains(canonical, rootPath) || pathContains(rootPath, canonical) {
			return Outcome{ExitCode: -1}, fmt.Errorf("%w: workspace overlaps a runtime mount", ErrSandboxUnavailable)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return Outcome{ExitCode: -1}, err
			}
			args = append(args, "--symlink", target, p)
		} else {
			args = append(args, "--ro-bind", p, p)
		}
	}
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--dir", "/workspace", "--bind", "/proc/self/fd/3", "/workspace", "--chdir", "/workspace", "--clearenv")
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		args = append(args, "--setenv", key, value)
	}
	newCommand := func(cctx context.Context, program string, programArgs []string) *exec.Cmd {
		argv := append(append([]string{}, args...), "--", program)
		argv = append(argv, programArgs...)
		cmd := exec.CommandContext(cctx, bwrap, argv...)
		cmd.Env = []string{"PATH=" + runtimePath, "LANG=C.UTF-8"}
		cmd.Dir = "/"
		cmd.ExtraFiles = []*os.File{root}
		return cmd
	}
	// Probe the exact isolation recipe before handing it user code. A failure is
	// always an execution refusal, never a reason to launch outside the sandbox.
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, err = capture(probeCtx, newCommand(probeCtx, "/bin/true", nil))
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return Outcome{ExitCode: -1}, ctx.Err()
		}
		return Outcome{ExitCode: -1}, fmt.Errorf("%w: bubblewrap could not establish isolation (%v); check namespace/seccomp/AppArmor policy; execution refused", ErrSandboxUnavailable, err)
	}
	program, err := resolveProgram(req.Program)
	if err != nil {
		return Outcome{ExitCode: -1}, err
	}
	return capture(ctx, newCommand(ctx, program, req.Args))
}
func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
