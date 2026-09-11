package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMutatingToolReturnsProducedArtifactErrors(t *testing.T) {
	root := t.TempDir()
	d := newDeliveryTracker(root)
	if err := d.declare([]string{"chart.svg", "future.json"}); err != nil {
		t.Fatal(err)
	}
	ctx := withDelivery(context.Background(), d)
	tool := EinoTool(retrievalFixture{"shell", func(context.Context, map[string]any) (string, error) {
		if err := os.WriteFile(filepath.Join(root, "chart.svg"), []byte(`<svg><rect></svg>`), 0600); err != nil {
			t.Fatal(err)
		}
		return "exit 0", nil
	}})
	got, err := tool.InvokableRun(ctx, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "chart.svg") || !strings.Contains(got, "structure") {
		t.Fatalf("successful shell hid broken SVG: %q", got)
	}
	if strings.Contains(got, "future.json") {
		t.Fatalf("unfinished files should not be reported as produced errors: %q", got)
	}
}

func TestProducedChecksSkipUnchangedAndRecheckRepairs(t *testing.T) {
	root := t.TempDir()
	d := newDeliveryTracker(root)
	if err := d.declare([]string{"result.json"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "result.json")
	if err := os.WriteFile(file, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if d.inspectProduced(ctx, "file_read") != "" {
		t.Fatal("read-only tools should not scan")
	}
	if d.inspectProduced(ctx, "shell") == "" {
		t.Fatal("missing broken JSON observation")
	}
	if d.inspectProduced(ctx, "shell") != "" {
		t.Fatal("unchanged revision was redundantly inspected")
	}
	if err := os.WriteFile(file, []byte(`{"fixed":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if d.inspectProduced(ctx, "file_write") != "" {
		t.Fatal("repair still reported broken")
	}
	if err := os.WriteFile(file, []byte(`{"broken"`), 0600); err != nil {
		t.Fatal(err)
	}
	if d.inspectProduced(ctx, "python") == "" {
		t.Fatal("changed broken revision was missed")
	}
}

func TestProducedChecksBoundWorkAndKeepFinalCheckFresh(t *testing.T) {
	root := t.TempDir()
	d := newDeliveryTracker(root)
	var paths []string
	for i := 0; i < automaticBatchFiles+2; i++ {
		name := fmt.Sprintf("%02d.json", i)
		paths = append(paths, name)
		if err := os.WriteFile(filepath.Join(root, name), []byte(`{`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	paths = append(paths, "large.json")
	if err := os.WriteFile(filepath.Join(root, "large.json"), []byte(strings.Repeat("{", automaticFileBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.declare(paths); err != nil {
		t.Fatal(err)
	}
	first := d.inspectProduced(context.Background(), "shell")
	if !strings.Contains(first, "00.json") || strings.Contains(first, "16.json") || strings.Contains(first, "large.json") {
		t.Fatalf("batch bounds wrong: %s", first)
	}
	second := d.inspectProduced(context.Background(), "shell")
	if !strings.Contains(second, "16.json") || strings.Contains(second, "00.json") {
		t.Fatalf("deferred batch lost: %s", second)
	}
	if failures := d.failures(context.Background()); len(failures) != len(paths) {
		t.Fatalf("final check trusted incremental cache: %v", failures)
	}
}

func TestProducedChecksDeferUndisplayedFailures(t *testing.T) {
	root := t.TempDir()
	d := newDeliveryTracker(root)
	var paths []string
	for i := 0; i < automaticBatchFiles; i++ {
		name := fmt.Sprintf("%02d_%s.json", i, strings.Repeat("x", 230))
		paths = append(paths, name)
		if err := os.WriteFile(filepath.Join(root, name), []byte(`{`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.declare(paths); err != nil {
		t.Fatal(err)
	}
	observed := ""
	for i := 0; i < automaticBatchFiles; i++ {
		notice := d.inspectProduced(context.Background(), "shell")
		if notice == "" {
			break
		}
		observed += notice
	}
	for _, p := range paths {
		if !strings.Contains(observed, p+": invalid JSON") {
			t.Errorf("diagnostic lost for %s", p)
		}
	}
	if got := d.inspectProduced(context.Background(), "shell"); got != "" {
		t.Fatal("already delivered errors repeated")
	}
}
