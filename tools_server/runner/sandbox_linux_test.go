//go:build linux

package runner

import (
	"context"
	"errors"
	"github.com/orka-oss/orka_core/delivery"
	"github.com/orka-oss/orka_core/pathsafe"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func strictConfig(t *testing.T) Config {
	t.Helper()
	cfg := FromEnv()
	cfg.Mode = "bwrap"
	_, err := cfg.Run(context.Background(), Request{Root: t.TempDir(), Program: "true"})
	if errors.Is(err, ErrSandboxUnavailable) && os.Getenv("ORKA_REQUIRE_SANDBOX_TEST") != "1" {
		t.Skip("bwrap unavailable; set ORKA_REQUIRE_SANDBOX_TEST=1 in sandbox CI to require execution")
	}
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSandboxRealSessionBoundary(t *testing.T) {
	cfg := strictConfig(t)
	base := t.TempDir()
	own := filepath.Join(base, "fake-a@example.test", "sessions", "one")
	otherSession := filepath.Join(base, "fake-a@example.test", "sessions", "two", "private.txt")
	otherOwner := filepath.Join(base, "fake-b@example.test", "sessions", "one", "private.txt")
	for _, p := range []string{filepath.Join(own, "own.txt"), otherSession, otherOwner} {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("synthetic-fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(otherOwner, filepath.Join(own, "escape")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORKA_TEST_FAKE_SECRET", "fixture-only")
	script := `
import os,sys,pathlib
assert os.getcwd() == '/workspace'
assert os.environ['HOME'] == '/workspace'
assert os.environ.get('ORKA_TEST_FAKE_SECRET') is None
assert pathlib.Path('own.txt').read_text() == 'synthetic-fixture'
for path in sys.argv[1:] + ['escape','../two/private.txt','/proc/1/root'+sys.argv[1]]:
    try:
        open(path).read()
    except (OSError, PermissionError):
        pass
    else:
        raise AssertionError('foreign fixture was visible')
assert b'ORKA_TEST_FAKE_SECRET=' not in pathlib.Path('/proc/1/environ').read_bytes()
assert not pathlib.Path('/run').exists()
pathlib.Path('created.txt').write_text('sandbox-write')
print('isolated')
`
	out, err := cfg.Run(context.Background(), Request{Root: own, Program: "python3", Args: []string{"-c", script, otherOwner, otherSession}, Timeout: 5 * time.Second})
	if err != nil || strings.TrimSpace(out) != "isolated" {
		t.Fatalf("strict boundary: %v %s", err, out)
	}
	if b, err := os.ReadFile(filepath.Join(own, "created.txt")); err != nil || string(b) != "sandbox-write" {
		t.Fatalf("own write missing: %v", err)
	}
	for _, p := range []string{otherOwner, otherSession} {
		b, _ := os.ReadFile(p)
		if string(b) != "synthetic-fixture" {
			t.Fatal("foreign file changed")
		}
	}
}

func TestSandboxCannotReachHostLoopback(t *testing.T) {
	cfg := strictConfig(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	script := `import socket,sys
s=socket.socket();s.settimeout(0.2)
try: s.connect(('127.0.0.1',int(sys.argv[1])))
except OSError: print('network-isolated')
else: raise AssertionError('host network exposed')`
	out, err := cfg.Run(context.Background(), Request{Root: t.TempDir(), Program: "python3", Args: []string{"-c", script, strconv.Itoa(port)}})
	if err != nil || strings.TrimSpace(out) != "network-isolated" {
		t.Fatalf("network boundary: %v %s", err, out)
	}
}

func TestSandboxFailureNeverExecutesUserCode(t *testing.T) {
	for _, mode := range []string{"bwrap", "misspelled"} {
		root := t.TempDir()
		cfg := Config{Mode: mode, BwrapPath: filepath.Join(root, "missing-bwrap")}
		_, err := cfg.Run(context.Background(), Request{Root: root, Program: "sh", Args: []string{"-c", "touch executed"}})
		if !errors.Is(err, ErrSandboxUnavailable) {
			t.Fatalf("expected explicit refusal: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "executed")); !os.IsNotExist(err) {
			t.Fatal("user code ran after sandbox failure")
		}
	}
	// Simulate a host/container rejecting namespace setup. The fixture launcher
	// exits before user code; it is not an isolation substitute.
	root := t.TempDir()
	launcher := filepath.Join(t.TempDir(), "unavailable-bwrap")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := (Config{Mode: "bwrap", BwrapPath: launcher}).Run(context.Background(), Request{Root: root, Program: "sh", Args: []string{"-c", "touch executed"}})
	if !errors.Is(err, ErrSandboxUnavailable) {
		t.Fatalf("failed setup did not refuse: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "executed")); !os.IsNotExist(err) {
		t.Fatal("user code ran after failed setup")
	}
}

func TestSandboxCancellationKillsNamespace(t *testing.T) {
	cfg := strictConfig(t)
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := cfg.Run(ctx, Request{Root: root, Program: "python3", Args: []string{"-c", `import os,time,pathlib
if os.fork()==0:
 os.setsid();time.sleep(1);pathlib.Path('escaped').write_text('bad');time.sleep(20)
else:
 pathlib.Path('started').write_text('yes');time.sleep(20)`}, Timeout: 5 * time.Second})
		done <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(root, "started")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(root, "started")); err != nil {
		t.Fatal("sandbox did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not release runner")
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "escaped")); !os.IsNotExist(err) {
		t.Fatal("child escaped cancellation")
	}
}

func TestSandboxCannotAccessFixedDeliveries(t *testing.T) {
	cfg := strictConfig(t)
	base := t.TempDir()
	root, err := pathsafe.EnsureSession(base, "fixture@example.test", "one")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "report.txt"), []byte("accepted"), 0600)
	if _, err := delivery.Publish(base, "fixture@example.test", "one", "run", []string{"report.txt"}); err != nil {
		t.Fatal(err)
	}
	script := `import pathlib,sys
assert pathlib.Path('report.txt').read_text() == 'accepted'
assert not pathlib.Path(sys.argv[1]).exists()
assert not pathlib.Path('/workspace/../.orka_deliveries').exists()
print('private-delivery')`
	out, err := cfg.Run(context.Background(), Request{Root: root, Program: "python3", Args: []string{"-c", script, filepath.Join(base, delivery.StoreDir)}})
	if err != nil || strings.TrimSpace(out) != "private-delivery" {
		t.Fatalf("delivery store exposed: %v %s", err, out)
	}
}
