package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/orka-oss/orka_core/pathsafe"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	root, err := pathsafe.EnsureSession(base, "fixture@example.test", "one")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "outputs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outputs/report.txt"), []byte("accepted-v1"), 0600); err != nil {
		t.Fatal(err)
	}
	return base, root
}
func TestPublishIsImmutableAndScoped(t *testing.T) {
	base, root := fixture(t)
	manifest, err := Publish(base, "fixture@example.test", "one", "run-one", []string{"outputs/report.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RunID != "run-one" || manifest.ConversationID != "one" || len(manifest.Files) != 1 || manifest.Files[0].SHA256 == "" || manifest.Files[0].Size != 11 {
		t.Fatalf("manifest=%+v", manifest)
	}
	os.WriteFile(filepath.Join(root, "outputs/report.txt"), []byte("changed-v2"), 0600)
	b, err := Read(base, "fixture@example.test", "one", "run-one", "outputs/report.txt")
	if err != nil || string(b) != "accepted-v1" {
		t.Fatalf("snapshot=%q %v", b, err)
	}
	if _, err := Publish(base, "fixture@example.test", "one", "run-one", []string{"outputs/report.txt"}); !errors.Is(err, ErrExists) {
		t.Fatalf("same run rewritten: %v", err)
	}
	if _, err := Read(base, "other@example.test", "one", "run-one", "outputs/report.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross owner=%v", err)
	}
	if _, err := Read(base, "fixture@example.test", "two", "run-one", "outputs/report.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross session=%v", err)
	}
	all, err := List(base, "fixture@example.test", "one")
	if err != nil || len(all) != 1 {
		t.Fatalf("list=%+v %v", all, err)
	}
}
func TestPublishRejectsPathsAndMissingFilesWithoutPartialDelivery(t *testing.T) {
	for _, p := range []string{"../two/private.txt", "/etc/passwd", "outputs/../../escape", "outputs\\escape", "missing.txt", "outputs"} {
		t.Run(p, func(t *testing.T) {
			base, _ := fixture(t)
			if _, err := Publish(base, "fixture@example.test", "one", "run", []string{"outputs/report.txt", p}); err == nil {
				t.Fatal("invalid source accepted")
			}
			all, err := List(base, "fixture@example.test", "one")
			if err != nil || len(all) != 0 {
				t.Fatalf("partial published: %+v %v", all, err)
			}
		})
	}
	base, root := fixture(t)
	outside := filepath.Join(t.TempDir(), "synthetic.txt")
	os.WriteFile(outside, []byte("other"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	if _, err := Publish(base, "fixture@example.test", "one", "run", []string{"escape"}); err == nil {
		t.Fatal("foreign symlink accepted")
	}
	if _, err := Publish(base, "fixture@example.test", "one", "../run", []string{"outputs/report.txt"}); err == nil {
		t.Fatal("run traversal accepted")
	}
}
func TestConcurrentPublishHasOneWinner(t *testing.T) {
	base, _ := fixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Publish(base, "fixture@example.test", "one", "run", []string{"outputs/report.txt"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrExists) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("publish winners=%d", wins)
	}
}

func TestReadDetectsStorageCorruption(t *testing.T) {
	base, _ := fixture(t)
	if _, err := Publish(base, "fixture@example.test", "one", "run", []string{"outputs/report.txt"}); err != nil {
		t.Fatal(err)
	}
	ownerHash := sha256.Sum256([]byte("fixture@example.test"))
	snapshot := filepath.Join(base, StoreDir, hex.EncodeToString(ownerHash[:]), "one", "run", "files", "outputs", "report.txt")
	// Simulate storage-admin corruption, not a session write: published files are
	// read-only and the snapshot directory is outside the executable workspace.
	if err := os.Chmod(snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot, []byte("corruption"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(base, "fixture@example.test", "one", "run", "outputs/report.txt"); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("corrupt snapshot accepted: %v", err)
	}
}
