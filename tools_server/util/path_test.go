package util

import (
	"path/filepath"
	"testing"
)

func TestResolvePathNormalizesAbsolute(t *testing.T) {
	base := t.TempDir()
	// an absolute-looking path collapses to its basename at the user root
	got, err := ResolvePath(base, "u@x.com", "/root/.openclaw/workspace/report.md")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "report.md" {
		t.Errorf("basename = %q", filepath.Base(got))
	}
	// it must live directly under the user root, not nested
	parent := filepath.Dir(got)
	if filepath.Base(parent) == "workspace" {
		t.Errorf("absolute path was nested, not flattened: %q", got)
	}
}

func TestResolvePathKeepsRelative(t *testing.T) {
	base := t.TempDir()
	got, err := ResolvePath(base, "u@x.com", "notes/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(got)) != "notes" {
		t.Errorf("relative subdir not preserved: %q", got)
	}
}

func TestResolvePathRejectsTraversal(t *testing.T) {
	base := t.TempDir()
	if _, err := ResolvePath(base, "u@x.com", "../../etc/passwd"); err == nil {
		t.Error("traversal should be rejected")
	}
}
