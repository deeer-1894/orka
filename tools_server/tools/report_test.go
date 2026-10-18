package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orka-oss/tools_server/identity"
)

func TestReportRenderingIsSessionScopedAndDoesNotOverwriteOnError(t *testing.T) {
	base := t.TempDir()
	ctx := identity.With(context.Background(), identity.Identity{Email: "reader", ConversationID: "a"})
	a := filepath.Join(base, "reader", "sessions", "a")
	b := filepath.Join(base, "reader", "sessions", "b")
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	spec := `{"kind":"orka.report/v1","output":"report.md","template":"Minimum {{n}}","bindings":{"n":{"csv":"data.csv","column":"n","op":"min"}}}`
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(a, "test.report.json"), spec)
	write(filepath.Join(a, "data.csv"), "n\n18\n19\n")
	write(filepath.Join(b, "data.csv"), "n\n999\n")
	invoke := func() bool {
		t.Helper()
		result, err := renderReport(base)(ctx, callWith(map[string]any{"path": "test.report.json"}))
		if err != nil {
			t.Fatal(err)
		}
		return result.IsError
	}
	if invoke() {
		t.Fatal("render failed")
	}
	body, err := os.ReadFile(filepath.Join(a, "report.md"))
	if err != nil || string(body) != "Minimum 18" {
		t.Fatalf("wrong session result %s %v", body, err)
	}
	write(filepath.Join(a, "data.csv"), "n\nNaN\n")
	if !invoke() {
		t.Fatal("bad numeric source accepted")
	}
	body, _ = os.ReadFile(filepath.Join(a, "report.md"))
	if string(body) != "Minimum 18" {
		t.Fatal("failed rendering overwrote previous report")
	}
	// The FS capability must reject symlinks into another session.
	if err := os.Remove(filepath.Join(a, "data.csv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(b, "data.csv"), filepath.Join(a, "data.csv")); err != nil {
		t.Fatal(err)
	}
	if !invoke() {
		t.Fatal("cross-session symlink read")
	}
}
func TestAtomicReportPublicationReplacesSymlinkAndRejectsCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.csv"), []byte("n\n18\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("source.csv", filepath.Join(dir, "report.md")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := writeRenderedReport(context.Background(), root, "report.md", "report"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "source.csv"))
	if string(body) != "n\n18\n" {
		t.Fatal("output symlink overwrote source")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writeRenderedReport(ctx, root, "report.md", "replacement"); err != context.Canceled {
		t.Fatalf("cancel=%v", err)
	}
	body, _ = os.ReadFile(filepath.Join(dir, "report.md"))
	if string(body) != "report" {
		t.Fatal("cancelled write published")
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatal("leaked temporary file")
		}
	}
}

func TestReportHandlerPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := renderReport(t.TempDir())(ctx, callWith(map[string]any{"path": "missing.report.json"}))
	if err != context.Canceled || result != nil {
		t.Fatalf("cancel must terminate, got result=%v err=%v", result, err)
	}
}
