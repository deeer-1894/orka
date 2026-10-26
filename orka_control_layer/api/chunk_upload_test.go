package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChunkUploadsStageOnDiskAssembleAndExpire(t *testing.T) {
	base, ws := t.TempDir(), t.TempDir()
	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	cm := newChunkManager()
	cm.sweeper.Do(func() {}) // no background sweeper in the test

	if done, n, err := cm.accept(base, "a@x", "k1", "docs/big.txt", 2, 1, []byte("world")); err != nil || done || n != 1 {
		t.Fatalf("first part: %v %d %v", done, n, err)
	}
	if got, total, ok := cm.progress(base, "a@x", "k1"); !ok || got != 1 || total != 2 {
		t.Fatalf("progress %d/%d %v", got, total, ok)
	}
	if _, _, err := cm.accept(base, "a@x", "k1", "other.txt", 2, 0, []byte("x")); err == nil {
		t.Fatal("changed metadata must be refused")
	}
	done, _, err := cm.accept(base, "a@x", "k1", "docs/big.txt", 2, 0, []byte("hello "))
	if err != nil || !done {
		t.Fatalf("second part: %v %v", done, err)
	}
	size, err := cm.assemble(base, "a@x", "k1", root, "docs/big.txt")
	if err != nil || size != 11 {
		t.Fatalf("assemble %d %v", size, err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "docs/big.txt")); string(b) != "hello world" {
		t.Fatalf("assembled %q", b)
	}
	if _, _, ok := cm.progress(base, "a@x", "k1"); ok {
		t.Fatal("staging must be removed after assembly")
	}

	// A user cannot hold more than maxUploadsPerUser unfinished uploads.
	for i := 0; i < maxUploadsPerUser; i++ {
		if _, _, err := cm.accept(base, "b@x", "k"+string(rune('a'+i)), "f.txt", 3, 0, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := cm.accept(base, "b@x", "one-more", "f.txt", 3, 0, []byte("x")); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("pending cap not enforced: %v", err)
	}
	// Abandoned uploads are swept, freeing the user's slots.
	cm.now = func() time.Time { return time.Now().Add(2 * abandonedAfter) }
	if removed := cm.sweep(base); removed != maxUploadsPerUser {
		t.Fatalf("swept %d, want %d", removed, maxUploadsPerUser)
	}
}

func TestChunkUploadSizeIsCapped(t *testing.T) {
	base := t.TempDir()
	cm := newChunkManager()
	cm.sweeper.Do(func() {})
	big := make([]byte, maxUploadBytes/2+1)
	if _, _, err := cm.accept(base, "a@x", "k", "f.bin", 3, 0, big); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cm.accept(base, "a@x", "k", "f.bin", 3, 1, big); err == nil {
		t.Fatal("an upload past the size cap must be refused")
	}
}
