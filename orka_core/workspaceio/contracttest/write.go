// Package contracttest is shared test support for workspace write adapters.
package contracttest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/orka-oss/orka_core/workspaceio"
)

type Writer func(map[string]any) error
type Factory func(*testing.T) (string, Writer)

func Run(t *testing.T, factory Factory) {
	t.Helper()
	for _, tc := range []struct {
		name   string
		args   map[string]any
		want   string
		reject bool
	}{
		{"default", map[string]any{"content": "new"}, "old", true},
		{"create", map[string]any{"mode": "create", "content": "new"}, "old", true},
		{"append", map[string]any{"mode": "append", "content": "+新"}, "old+新", false},
		{"replace", map[string]any{"mode": "replace", "content": "new"}, "new", false},
		{"empty replacement", map[string]any{"mode": "replace", "content": ""}, "", false},
		{"quoted marker", map[string]any{"mode": "replace", "content": "Quote <persisted-arg>x</persisted-arg> example"}, "Quote <persisted-arg>x</persisted-arg> example", false},
		{"missing", map[string]any{"mode": "replace"}, "old", true},
		{"null", map[string]any{"mode": "replace", "content": nil}, "old", true},
		{"number", map[string]any{"mode": "replace", "content": 2}, "old", true},
		{"arg placeholder", map[string]any{"mode": "replace", "content": " <persisted-arg>x</persisted-arg>\n"}, "old", true},
		{"output placeholder", map[string]any{"mode": "append", "content": "<persisted-output>x</persisted-output>"}, "old", true},
		{"bad mode", map[string]any{"mode": "overwrite", "content": "new"}, "old", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, write := factory(t)
			p := filepath.Join(root, "note.txt")
			if err := os.WriteFile(p, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			tc.args["path"] = "note.txt"
			if err := write(tc.args); (err != nil) != tc.reject {
				t.Fatalf("reject=%v want %v", err, tc.reject)
			}
			b, err := os.ReadFile(p)
			if err != nil || string(b) != tc.want {
				t.Fatalf("bytes=%q err=%v", b, err)
			}
			versions, _ := filepath.Glob(filepath.Join(root, workspaceio.TrashDir, "*", "note.txt"))
			want := 1
			if tc.reject {
				want = 0
			}
			if len(versions) != want {
				t.Fatalf("versions=%d want %d", len(versions), want)
			}
			if want == 1 {
				b, _ := os.ReadFile(versions[0])
				if string(b) != "old" {
					t.Fatal("backup is not original bytes")
				}
				if _, err := workspaceio.ParseVersion(filepath.Base(filepath.Dir(versions[0]))); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	t.Run("concurrent create", func(t *testing.T) {
		root, write := factory(t)
		done := make(chan error, 16)
		for i := 0; i < 16; i++ {
			go func(i int) { done <- write(map[string]any{"path": "race.txt", "content": fmt.Sprint(i)}) }(i)
		}
		wins := 0
		for i := 0; i < 16; i++ {
			if <-done == nil {
				wins++
			}
		}
		if wins != 1 {
			t.Fatalf("creators=%d", wins)
		}
		if _, err := os.Stat(filepath.Join(root, workspaceio.TrashDir)); !os.IsNotExist(err) {
			t.Fatal("create race wrote history")
		}
	})
	t.Run("foreign symlink", func(t *testing.T) {
		root, write := factory(t)
		outside := filepath.Join(t.TempDir(), "fixture.txt")
		os.WriteFile(outside, []byte("other"), 0600)
		if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
			t.Skip(err)
		}
		if err := write(map[string]any{"path": "escape", "mode": "replace", "content": "bad"}); err == nil {
			t.Fatal("foreign symlink accepted")
		}
		b, _ := os.ReadFile(outside)
		if string(b) != "other" {
			t.Fatal("foreign fixture modified")
		}
	})
}
