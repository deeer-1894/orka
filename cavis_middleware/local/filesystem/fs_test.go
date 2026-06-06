package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileTools_WriteReadList(t *testing.T) {
	root := t.TempDir()
	tools := New(root)
	ctx := context.Background()

	var write, read, list = tools[1], tools[0], tools[2]
	if write.Name() != "file_write" || read.Name() != "file_read" || list.Name() != "file_list" {
		t.Fatalf("unexpected tool order: %s %s %s", tools[0].Name(), tools[1].Name(), tools[2].Name())
	}

	if _, err := write.Invoke(ctx, map[string]any{"path": "sub/a.txt", "content": "hello"}); err != nil {
		t.Fatal(err)
	}
	// file actually on disk under root
	if b, err := os.ReadFile(filepath.Join(root, "sub/a.txt")); err != nil || string(b) != "hello" {
		t.Fatalf("disk content = %q err=%v", b, err)
	}
	got, err := read.Invoke(ctx, map[string]any{"path": "sub/a.txt"})
	if err != nil || got != "hello" {
		t.Fatalf("read = %q err=%v", got, err)
	}
	out, err := list.Invoke(ctx, map[string]any{"path": "sub"})
	if err != nil || !strings.Contains(out, "a.txt") {
		t.Fatalf("list = %q err=%v", out, err)
	}
}

func TestFileTools_RejectTraversal(t *testing.T) {
	root := t.TempDir()
	tools := New(root)
	ctx := context.Background()
	write := tools[1]
	if _, err := write.Invoke(ctx, map[string]any{"path": "../../etc/evil", "content": "x"}); err == nil {
		t.Fatal("expected traversal write to be rejected")
	}
	read := tools[0]
	if _, err := read.Invoke(ctx, map[string]any{"path": "../../../etc/passwd"}); err == nil {
		t.Fatal("expected traversal read to be rejected")
	}
}
