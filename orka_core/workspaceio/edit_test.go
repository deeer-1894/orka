package workspaceio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditReplacesExactlyOneMatchAndKeepsHistory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ratelimit.py")
	if err := os.WriteFile(path, []byte("def a():\n    capacity = None\n\ndef b():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	intent, err := ParseIntent(map[string]any{"mode": "edit", "old_text": "capacity = None", "new_text": "capacity = rate * 2"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(root, "ratelimit.py", intent)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "capacity = rate * 2") || strings.Contains(string(got), "capacity = None") || res.Version == "" {
		t.Fatalf("edit result %q version %q", got, res.Version)
	}

	for _, c := range []struct{ old, why string }{{"not there", "not found"}, {"def ", "appears 2 times"}} {
		in, _ := ParseIntent(map[string]any{"mode": "edit", "old_text": c.old, "content": "x"})
		if _, err := Apply(root, "ratelimit.py", in); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Fatalf("old_text %q: err %v, want %q", c.old, err, c.why)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(got) {
		t.Fatal("a refused edit must leave the file untouched")
	}
	if _, err := ParseIntent(map[string]any{"mode": "edit", "new_text": "x"}); err == nil {
		t.Fatal("edit needs old_text")
	}
	if in, err := ParseIntent(map[string]any{"mode": "edit", "old_text": "a", "content": "b"}); err != nil || in.Content != "b" {
		t.Fatalf("content is still accepted as the replacement: %+v %v", in, err)
	}
	if in, err := ParseIntent(map[string]any{"mode": "edit", "old_text": "a", "new_text": ""}); err != nil || in.Content != "" {
		t.Fatalf("an empty new_text deletes the old text: %+v %v", in, err)
	}
	in, _ := ParseIntent(map[string]any{"mode": "edit", "old_text": "a", "content": "b"})
	if _, err := Apply(root, "missing.py", in); err == nil {
		t.Fatal("edit needs an existing file")
	}
}
