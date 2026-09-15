package workspaceio

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedReplacementKeepsOriginalOnFailure(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "report.bin")
	os.WriteFile(target, []byte("old"), 0600)
	_, err := ReplaceGenerated(root, "report.bin", func(temp string) error {
		os.WriteFile(filepath.Join(root, temp), []byte("partial"), 0600)
		return errors.New("generator failed")
	})
	if err == nil {
		t.Fatal("generation failure accepted")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "old" {
		t.Fatal("failed generator replaced original")
	}
	versions, _ := filepath.Glob(filepath.Join(root, TrashDir, "*", "report.bin"))
	if len(versions) != 0 {
		t.Fatal("failed generation created history")
	}
}
func TestGeneratedReplacementPublishesWithBackup(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "report.bin"), []byte("old"), 0600)
	result, err := ReplaceGenerated(root, "report.bin", func(temp string) error {
		b, _ := os.ReadFile(filepath.Join(root, "report.bin"))
		if string(b) != "old" {
			t.Fatal("target changed before publication")
		}
		return os.WriteFile(filepath.Join(root, temp), []byte{0, 1, 2, 255}, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "replace" || result.Version == "" {
		t.Fatalf("receipt=%+v", result)
	}
	b, _ := os.ReadFile(filepath.Join(root, "report.bin"))
	if len(b) != 4 || b[3] != 255 {
		t.Fatal("binary changed")
	}
	b, _ = os.ReadFile(filepath.Join(root, TrashDir, result.Version, "report.bin"))
	if string(b) != "old" {
		t.Fatal("missing original version")
	}
}
func TestGeneratedReplacementRefusesWithoutBackup(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "report.bin"), []byte("old"), 0600)
	os.WriteFile(filepath.Join(root, TrashDir), []byte("blocks-backup"), 0600)
	_, err := ReplaceGenerated(root, "report.bin", func(temp string) error { return os.WriteFile(filepath.Join(root, temp), []byte("new"), 0600) })
	if err == nil {
		t.Fatal("replaced despite failed history")
	}
	b, _ := os.ReadFile(filepath.Join(root, "report.bin"))
	if string(b) != "old" {
		t.Fatal("lost original on backup failure")
	}
}
