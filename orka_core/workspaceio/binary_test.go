package workspaceio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPublishBinaryConcurrentCreateAndNoOverwrite(t *testing.T) {
	root := t.TempDir()
	start := make(chan struct{})
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := PublishBinary(context.Background(), root, "out/file.bin", "", bytes.NewReader([]byte{0, 1, 2, 255}), 16)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, fs.ErrExist) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("create winners=%d", wins)
	}
	data, err := os.ReadFile(filepath.Join(root, "out/file.bin"))
	if err != nil || !bytes.Equal(data, []byte{0, 1, 2, 255}) {
		t.Fatalf("published bytes=%v %v", data, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "out" {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestPublishBinaryReplacementHistoryAndFailureAtomicity(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	original := []byte{0, 1, 255}
	next := []byte{2, 3, 0}
	if _, err := PublishBinary(ctx, root, "file.bin", "create", bytes.NewReader(original), 3); err != nil {
		t.Fatal(err)
	}
	result, err := PublishBinary(ctx, root, "file.bin", "replace", bytes.NewReader(next), 3)
	if err != nil || result.Version == "" {
		t.Fatalf("replace receipt: %+v %v", result, err)
	}
	old, err := os.ReadFile(filepath.Join(root, TrashDir, result.Version, "file.bin"))
	if err != nil || !bytes.Equal(old, original) {
		t.Fatalf("history=%v %v", old, err)
	}
	_, err = PublishBinary(ctx, root, "file.bin", "replace", bytes.NewReader([]byte("oversized")), 3)
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("size limit: %v", err)
	}
	actual, _ := os.ReadFile(filepath.Join(root, "file.bin"))
	if !bytes.Equal(actual, next) {
		t.Fatal("failed replace changed bytes")
	}
	entries, _ := filepath.Glob(filepath.Join(root, ".orka-generate-*"))
	if len(entries) != 0 {
		t.Fatal("partial staging survived")
	}
}

type cancelBinaryReader struct{ cancel context.CancelFunc }

func (r cancelBinaryReader) Read(p []byte) (int, error) {
	r.cancel()
	copy(p, "partial")
	return 7, nil
}

func TestPublishBinaryCancellationAndBoundsLeaveNoFile(t *testing.T) {
	for _, kind := range []string{"cancel", "limit", "reader_error"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var source io.Reader = bytes.NewReader([]byte("too large"))
			expected := ErrOutputLimit
			if kind == "cancel" {
				source = cancelBinaryReader{cancel}
				expected = context.Canceled
			}
			if kind == "reader_error" {
				source = io.MultiReader(bytes.NewReader([]byte("a")), errorBinaryReader{})
				expected = io.ErrUnexpectedEOF
			}
			_, err := PublishBinary(ctx, root, "out/new.bin", "", source, 8)
			if !errors.Is(err, expected) {
				t.Fatalf("error=%v expected=%v", err, expected)
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 0 {
				t.Fatalf("failure left output: %v", entries)
			}
		})
	}
}

type errorBinaryReader struct{}

func (errorBinaryReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestPublishBinaryRejectsTraversalSymlinksAndAppend(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "other-session")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../outside.bin", "/absolute.bin", "other-session/file.bin", "a/../file.bin", "a\\file.bin", "."} {
		if _, err := PublishBinary(context.Background(), root, p, "create", bytes.NewReader([]byte("data")), 4); err == nil {
			t.Fatalf("unsafe path accepted: %s", p)
		}
	}
	if _, err := PublishBinary(context.Background(), root, "file.bin", "append", bytes.NewReader(nil), 4); err == nil {
		t.Fatal("append accepted for binary publication")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("cross-session bytes written")
	}
}
