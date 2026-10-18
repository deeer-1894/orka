package delivery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/orka-oss/orka_core/artifacts"
)

func TestPublishCheckedValidatesCopiedFilesBeforeCommit(t *testing.T) {
	base, root := fixture(t)
	calls := 0
	_, err := PublishChecked(base, "fixture@example.test", "one", "checked", []string{"outputs/report.txt"}, func(snapshot fs.FS) error {
		calls++
		if err := os.WriteFile(filepath.Join(root, "outputs/report.txt"), []byte("live-changed"), 0600); err != nil {
			return err
		}
		b, err := fs.ReadFile(snapshot, "outputs/report.txt")
		if err != nil || string(b) != "accepted-v1" {
			return fmt.Errorf("validated live bytes: %q %v", b, err)
		}
		if _, err := fs.ReadFile(snapshot, "../manifest.json"); err == nil {
			return fmt.Errorf("snapshot escaped")
		}
		listed, err := List(base, "fixture@example.test", "one")
		if err != nil || len(listed) != 0 {
			return fmt.Errorf("visible before validation: %v %v", listed, err)
		}
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	b, err := Read(base, "fixture@example.test", "one", "checked", "outputs/report.txt")
	if err != nil || string(b) != "accepted-v1" {
		t.Fatalf("read=%q %v", b, err)
	}
}

func TestPublishCheckedFailureRemovesUncommittedRun(t *testing.T) {
	base, _ := fixture(t)
	denied := errors.New("not accepted")
	_, err := PublishChecked(base, "fixture@example.test", "one", "checked", []string{"outputs/report.txt"}, func(fs.FS) error { return denied })
	if !errors.Is(err, denied) {
		t.Fatalf("validation error lost: %v", err)
	}
	listed, err := List(base, "fixture@example.test", "one")
	if err != nil || len(listed) != 0 {
		t.Fatalf("rejected delivery visible: %v %v", listed, err)
	}
	if _, err := Read(base, "fixture@example.test", "one", "checked", "outputs/report.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected delivery readable: %v", err)
	}
	if _, err := PublishChecked(base, "fixture@example.test", "one", "checked", []string{"outputs/report.txt"}, func(fs.FS) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishChecked(base, "fixture@example.test", "one", "nil-check", []string{"outputs/report.txt"}, nil); err == nil {
		t.Fatal("nil checker accepted")
	}
}

func TestPublishCheckedRequiresDeclaredDependencies(t *testing.T) {
	base, root := fixture(t)
	if err := os.WriteFile(filepath.Join(root, "outputs/index.html"), []byte(`<img src="report.txt">`), 0600); err != nil {
		t.Fatal(err)
	}
	check := func(snapshot fs.FS) error {
		report := artifacts.CheckFS(context.Background(), snapshot, []string{"outputs/index.html"})
		if !report.OK {
			return fmt.Errorf("%v", report.Failures)
		}
		return nil
	}
	if _, err := PublishChecked(base, "fixture@example.test", "one", "checked", []string{"outputs/index.html"}, check); err == nil {
		t.Fatal("undeclared live dependency accepted")
	}
	if _, err := PublishChecked(base, "fixture@example.test", "one", "checked", []string{"outputs/index.html", "outputs/report.txt"}, check); err != nil {
		t.Fatal(err)
	}
}
