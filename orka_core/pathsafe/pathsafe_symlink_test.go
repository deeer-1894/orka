package pathsafe

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A linked directory ABOVE the root is the deployment's business (macOS /var,
// a mounted volume); a link at or below the root is something a sandboxed
// program could have planted, and stays refused.
func TestResolveTrustsLinksAboveRootOnly(t *testing.T) {
	real := t.TempDir()
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "storage")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(link, "user")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(root, "sessions/c1/report.md")
	if err != nil {
		t.Fatalf("a link above the root must be allowed: %v", err)
	}
	if got != filepath.Join(root, "sessions/c1/report.md") {
		t.Fatalf("resolved path changed: %s", got)
	}

	if err := os.Symlink("/etc", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(root, "escape/passwd"); !errors.Is(err, ErrEscapes) || !errors.Is(err, ErrSymlink) {
		t.Fatalf("a link below the root must be refused as an escape, got %v", err)
	}
	if _, err := Resolve(filepath.Join(root, "escape"), "passwd"); !errors.Is(err, ErrSymlink) {
		t.Fatalf("a root that is itself a link must be refused, got %v", err)
	}
}
