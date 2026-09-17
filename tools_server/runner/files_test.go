package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestExecutionObservesFilesWithoutGuessingStdout(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "input.txt"), []byte("keep"), 0600)
	os.Symlink(t.TempDir(), filepath.Join(dir, "outside"))
	result, err := (Config{Mode: "unsafe-dev"}).Execute(context.Background(), Request{
		Root: dir, Program: "bash", Args: []string{"-c", `mkdir -p output; printf report > output/report.html; printf zip > package.zip; printf 'input.txt'; exit 7`}, TrackFiles: true,
	})
	if err == nil || result.OK || result.FileChanges == nil || result.FileChanges.Partial {
		t.Fatalf("%+v", result)
	}
	if !slices.Equal(result.FileChanges.Paths, []string{"output/report.html", "package.zip"}) {
		t.Fatalf("%+v", result.FileChanges)
	}
}

func TestInventoryIsBoundedAndSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	os.Symlink("/etc/passwd", filepath.Join(dir, "linked.txt"))
	os.Mkdir(filepath.Join(dir, ".cache"), 0700)
	os.WriteFile(filepath.Join(dir, ".cache", "hidden.txt"), nil, 0600)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	got := inventory(context.Background(), root)
	if got.partial || len(got.files) != 0 {
		t.Fatalf("%+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !inventory(ctx, root).partial {
		t.Fatal("cancel must mark incomplete observation")
	}
}
