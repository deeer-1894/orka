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

func TestFileTools_BackupOnOverwrite(t *testing.T) {
	root := t.TempDir()
	tools := New(root)
	ctx := context.Background()
	write := tools[1]

	// First write: no prior version → no backup.
	if _, err := write.Invoke(ctx, map[string]any{"path": "note.md", "content": "v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, trashDir)); !os.IsNotExist(err) {
		t.Fatal("trash should not exist after a first write")
	}

	// Overwrite: the old content must be preserved under .orka_trash/<ts>/note.md.
	out, err := write.Invoke(ctx, map[string]any{"path": "note.md", "content": "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, trashDir) {
		t.Fatalf("overwrite result should mention the backup, got %q", out)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "note.md")); string(b) != "v2" {
		t.Fatalf("current content = %q, want v2", b)
	}
	// find the backed-up v1 somewhere under the trash dir
	var foundV1 bool
	_ = filepath.Walk(filepath.Join(root, trashDir), func(p string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); string(b) == "v1" {
				foundV1 = true
			}
		}
		return nil
	})
	if !foundV1 {
		t.Fatal("previous version v1 not found in trash backup")
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
