//go:build unix

package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestDeclaredCheckRejectsFIFOBeforeOpen(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "evidence.log")
	if err := syscall.Mkfifo(target, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan Report, 1)
	go func() { done <- CheckDeclared(context.Background(), root, []string{"evidence.log"}) }()
	select {
	case report := <-done:
		if report.OK {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		// Unstick the old implementation so a failing regression does not leak a reader.
		f, _ := os.OpenFile(target, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if f != nil {
			f.Close()
		}
		t.Fatal("FIFO blocked delivery precheck")
	}
}
