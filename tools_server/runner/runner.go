// Package runner runs workspace processes with a clean environment, bounded
// output and cancellation. Linux isolation is mandatory unless an operator
// explicitly selects unsafe-dev. It never falls back after sandbox failure.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/orka-oss/orka_core/pathsafe"
)

const OutputLimit = 32 * 1024
const runtimePath = "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"

var ErrSandboxUnavailable = errors.New("sandbox unavailable")

type Config struct{ Mode, BwrapPath string }

func FromEnv() Config {
	mode := os.Getenv("CODE_SANDBOX_MODE")
	if mode == "" {
		mode = "bwrap"
	}
	return Config{Mode: mode, BwrapPath: os.Getenv("CODE_BWRAP_PATH")}
}

type Request struct {
	Root, Program string
	Args, Env     []string // Env contains explicit tool data, never os.Environ().
	Timeout       time.Duration
}

func (cfg Config) execute(ctx context.Context, req Request) (Outcome, error) {
	if cfg.Mode == "" {
		cfg.Mode = "bwrap"
	}
	if cfg.Mode != "bwrap" && cfg.Mode != "unsafe-dev" {
		return Outcome{ExitCode: -1}, fmt.Errorf("%w: unknown CODE_SANDBOX_MODE (use bwrap, or explicitly unsafe-dev for trusted local development)", ErrSandboxUnavailable)
	}
	rootPath, err := pathsafe.Resolve(req.Root, ".")
	if err != nil {
		return Outcome{ExitCode: -1}, err
	}
	pinned, err := os.Open(rootPath)
	if err != nil {
		return Outcome{ExitCode: -1}, err
	}
	defer pinned.Close()
	info, err := pinned.Stat()
	if err != nil {
		return Outcome{ExitCode: -1}, err
	}
	if !info.IsDir() {
		return Outcome{ExitCode: -1}, fmt.Errorf("workspace must be a directory")
	}
	timeout := req.Timeout
	if timeout <= 0 || timeout > 120*time.Second {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	home := "/workspace"
	if cfg.Mode == "unsafe-dev" {
		home = rootPath
	}
	env, err := cleanEnv(home, req.Env)
	if err != nil {
		return Outcome{ExitCode: -1}, err
	}
	if cfg.Mode == "bwrap" {
		return cfg.runSandbox(ctx, req, rootPath, pinned, env)
	}
	program, err := resolveProgram(req.Program)
	if err != nil {
		return Outcome{ExitCode: -1}, err
	}
	cmd := exec.CommandContext(ctx, program, req.Args...)
	cmd.Dir = rootPath
	cmd.Env = env
	return capture(ctx, cmd)
}

func resolveProgram(program string) (string, error) {
	// Resolve only the trusted runtime PATH, never an inherited path or executable
	// planted in the workspace. User scripts are arguments to sh/python instead.
	if filepath.Base(program) != program || program == "." || program == "" {
		return "", fmt.Errorf("invalid runtime program")
	}
	for _, dir := range filepath.SplitList(runtimePath) {
		candidate := filepath.Join(dir, program)
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() && st.Mode()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("runtime program %q unavailable", program)
}

func cleanEnv(home string, extra []string) ([]string, error) {
	env := []string{"PATH=" + runtimePath, "HOME=" + home, "TMPDIR=/tmp", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC", "PYTHONDONTWRITEBYTECODE=1"}
	// Only explicit data parameters used by built-in office scripts may cross the
	// boundary. Loader/interpreter variables, proxy settings and credentials cannot.
	allowed := map[string]bool{}
	for _, key := range strings.Fields("CHART_DATA CHART_TYPE CHART_X CHART_Y CHART_AGG CHART_TITLE CHART_OUT XL_IN XL_SHEET XL_OUT CX_IN CX_OUT CX_SHEET SQL_QUERY SQL_TABLES SQL_OUT J_LEFT J_RIGHT J_ON J_LON J_RON J_HOW J_OUT SL_MD SL_TITLE SL_OUT") {
		allowed[key] = true
	}
	for _, entry := range extra {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !allowed[key] || strings.ContainsRune(entry, 0) {
			return nil, fmt.Errorf("unsupported process environment parameter")
		}
		env = append(env, entry)
	}
	return env, nil
}
