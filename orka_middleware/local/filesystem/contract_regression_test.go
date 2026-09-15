package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalWriteContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   map[string]any
		want   string
		reject bool
	}{
		{"default create", map[string]any{"content": "new"}, "original", true},
		{"explicit create", map[string]any{"mode": "create", "content": "new"}, "original", true},
		{"append", map[string]any{"mode": "append", "content": "+new"}, "original+new", false},
		{"replace", map[string]any{"mode": "replace", "content": "new"}, "new", false},
		{"missing content", map[string]any{"mode": "replace"}, "original", true},
		{"nonstring", map[string]any{"mode": "replace", "content": 42}, "original", true},
		{"placeholder", map[string]any{"mode": "replace", "content": "<persisted-arg>archive</persisted-arg>"}, "original", true},
		{"invalid mode", map[string]any{"mode": "overwrite", "content": "new"}, "original", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, "note.txt")
			if err := os.WriteFile(p, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			tc.args["path"] = "note.txt"
			_, err := New(root)[1].Invoke(context.Background(), tc.args)
			if (err != nil) != tc.reject {
				t.Errorf("rejection=%v want %v", err, tc.reject)
			}
			b, _ := os.ReadFile(p)
			if string(b) != tc.want {
				t.Errorf("content=%q want %q", b, tc.want)
			}
			versions, _ := filepath.Glob(filepath.Join(root, ".orka_trash", "*", "note.txt"))
			if tc.reject && len(versions) != 0 {
				t.Fatal("rejected write created history")
			}
			if !tc.reject {
				if len(versions) != 1 {
					t.Fatalf("versions=%v", versions)
				}
				stamp := filepath.Base(filepath.Dir(versions[0]))
				if _, err := time.Parse("20060102-150405.000000000", stamp); err != nil {
					t.Fatalf("incompatible version timestamp: %s", stamp)
				}
			}
		})
	}
}

func TestLocalWriteRejectsPlaceholderBeforeCreatingDirectories(t *testing.T) {
	root := t.TempDir()
	_, err := New(root)[1].Invoke(context.Background(), map[string]any{"path": "new/sub.txt", "content": "<persisted-output>archive</persisted-output>"})
	if err == nil {
		t.Fatal("placeholder accepted")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("invalid write changed filesystem")
	}
}

func TestLocalWriteSchemaHasIntent(t *testing.T) {
	tool := New(t.TempDir())[1]
	props := tool.Schema()["properties"].(map[string]any)
	if props["mode"] == nil || !strings.Contains(tool.Description(), "create") {
		t.Fatal("missing write-intent contract")
	}
}
