package pathsafe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(root, "escape/secret.txt"); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestCopySessionIsIndependentAndSkipsSymlinks(t *testing.T) {
	base := t.TempDir()
	src, err := EnsureSession(base, "owner", "one")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "report.txt"), []byte("original"), 0644)
	os.Symlink("/etc/passwd", filepath.Join(src, "secret"))
	skipped, err := CopySession(base, "owner", "one", "reader", "fork")
	if err != nil || len(skipped) != 1 {
		t.Fatalf("copy: %v %v", skipped, err)
	}
	dst, _ := SessionRoot(base, "reader", "fork")
	b, err := os.ReadFile(filepath.Join(dst, "report.txt"))
	if err != nil || string(b) != "original" {
		t.Fatalf("copy: %s %v", b, err)
	}
	os.WriteFile(filepath.Join(dst, "report.txt"), []byte("changed"), 0644)
	b, _ = os.ReadFile(filepath.Join(src, "report.txt"))
	if string(b) != "original" {
		t.Fatal("fork changed original")
	}
}
func TestSessionRootRejectsMissingAndMalformedContext(t *testing.T) {
	base := t.TempDir()
	for _, id := range []string{"", ".", "..", "../other", "one/two", "one\\two", " one", "one\x00"} {
		if _, err := SessionRoot(base, "user", id); err == nil {
			t.Errorf("accepted %q", id)
		}
	}
	for _, user := range []string{"", ".", "..", "../other", "a/b"} {
		if _, err := SessionRoot(base, user, "one"); err == nil {
			t.Errorf("accepted user %q", user)
		}
	}
}
