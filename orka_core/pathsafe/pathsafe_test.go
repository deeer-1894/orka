package pathsafe

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve_WithinRoot(t *testing.T) {
	root := t.TempDir()
	got, err := Resolve(root, "sub/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "sub/dir/file.txt")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolve_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	cases := []string{
		"../../etc/passwd",
		"../outside",
		"a/../../b",
		"/etc/passwd",          // absolute is neutralized -> stays in root
		"sub/../../../../escape",
	}
	for _, c := range cases {
		got, err := Resolve(root, c)
		if err == ErrEscapes {
			continue // correctly rejected
		}
		// absolute-input cases are neutralized rather than rejected; ensure
		// whatever resolved still lives under root.
		if err == nil {
			r, _ := filepath.Rel(root, got)
			if strings.HasPrefix(r, "..") {
				t.Fatalf("%q escaped root: %q", c, got)
			}
			continue
		}
		t.Fatalf("%q: unexpected err %v", c, err)
	}
}

func TestResolve_AbsoluteNeutralized(t *testing.T) {
	root := t.TempDir()
	got, err := Resolve(root, "/etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "etc/passwd") {
		t.Fatalf("absolute not neutralized: %q", got)
	}
}

func TestUserRoot_SingleSegment(t *testing.T) {
	r := UserRoot("/data/storage", "a@b.com")
	if r != filepath.Join("/data/storage", "a@b.com") {
		t.Fatalf("user root = %q", r)
	}
	// malicious user id cannot escape
	r2 := UserRoot("/data/storage", "../../etc")
	if strings.Contains(r2, "..") {
		t.Fatalf("user root not sanitized: %q", r2)
	}
}
