package workspaceio

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVersionFormatsAndPruning(t *testing.T) {
	for _, stamp := range []string{"20260915-120000", "20260915-120000.123456789"} {
		if _, err := ParseVersion(stamp); err != nil {
			t.Fatal(err)
		}
	}
	for _, stamp := range []string{"../20260915-120000", "20260915-120000/child", "20261315-120000", "20260915-120000.123", "20260915-120000,123456789"} {
		if _, err := ParseVersion(stamp); err == nil {
			t.Fatalf("invalid version accepted: %q", stamp)
		}
	}
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for i := 0; i < 25; i++ {
		if _, err := Snapshot(root, "nested/note.txt", []byte(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	versions, _ := filepath.Glob(filepath.Join(path, TrashDir, "*", "nested", "note.txt"))
	if len(versions) != MaxVersionsPerFile {
		t.Fatalf("versions=%d", len(versions))
	}
	b, _ := os.ReadFile(versions[0])
	if string(b) != "5" {
		t.Fatalf("oldest retained version=%q", b)
	}
}
