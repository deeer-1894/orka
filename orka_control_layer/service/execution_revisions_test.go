package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionEvidenceTracksVersionsWithoutClaimingAcceptance(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "deliverable.zip")
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("tested version")
	d := newDeliveryTracker(root)
	if err := d.declare([]string{"deliverable.zip"}); err != nil {
		t.Fatal(err)
	}
	scope := acceptanceScope{base: t.TempDir(), owner: "a", conversation: "b", runID: "c"}
	ctx := withDelivery(context.WithValue(context.Background(), acceptanceScopeKey{}, scope), d)
	files, partial := executionRevisions(ctx)
	if partial || len(files) != 1 {
		t.Fatalf("%+v %v", files, partial)
	}
	write("changed during command")
	recordExecution(ctx, "shell", nil, `{"ok":true,"exit_code":0,"stdout":"passed","stderr":""}`, executionRevisionSnapshot{files: files})
	records, _, err := readExecutionEvidence(scope)
	if err != nil || len(records) != 1 {
		t.Fatalf("%+v %v", records, err)
	}
	if len(records[0].ChangedDuring) != 1 {
		t.Fatalf("unrecorded mutation: %+v", records)
	}
	refreshExecutionHistory(ctx, root, records)
	if len(records[0].ChangedSince) != 1 {
		t.Fatalf("stale evidence not identified: %+v", records)
	}
	write("tested version")
	refreshExecutionHistory(ctx, root, records)
	if len(records[0].ChangedSince) != 0 || records[0].RevisionsPartial {
		t.Fatalf("unchanged bytes flagged: %+v", records)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	refreshExecutionHistory(ctx, root, records)
	if len(records[0].ChangedSince) != 1 {
		t.Fatal("deleted file accepted as unchanged")
	}
	history, err := readAcceptance(scope)
	if err != nil || len(history.Checks) != 0 {
		t.Fatal("execution manufactured acceptance", history, err)
	}
}

func TestExecutionRevisionsBoundedAndSessionConfined(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("private bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(root, "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(9 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	d := newDeliveryTracker(root)
	if err := d.declare([]string{"escape", "large"}); err != nil {
		t.Fatal(err)
	}
	files, partial := executionRevisions(withDelivery(context.Background(), d))
	if !partial || len(files) != 0 {
		t.Fatalf("unsafe or oversized files read: %+v %v", files, partial)
	}
}

func TestExecutionEvidenceDoesNotDefeatRepeatedCallDetection(t *testing.T) {
	ctx := context.WithValue(context.Background(), acceptanceScopeKey{}, acceptanceScope{base: t.TempDir(), owner: "a", conversation: "b", runID: "c"})
	ctx = withLoopDetector(ctx, newLoopDetector())
	tool := EinoTool(retrievalFixture{"shell", func(context.Context, map[string]any) (string, error) {
		return `{"ok":true,"exit_code":0,"stdout":"unchanged","stderr":""}`, nil
	}})
	var out string
	for i := 0; i < repeatBeforeNudge; i++ {
		var err error
		out, err = tool.InvokableRun(ctx, `{"command":"test"}`)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(out, "每次返回的有效结果都一样") {
		t.Fatal("unique evidence IDs disabled loop detection", out)
	}
}

func TestRepeatedExecutionFailureIsObservedWithoutSuppressingCalls(t *testing.T) {
	ctx := withLoopDetector(context.Background(), newLoopDetector())
	calls := 0
	tool := EinoTool(retrievalFixture{"shell", func(context.Context, map[string]any) (string, error) {
		calls++
		return "", errors.New(`tool "shell" error: {"ok":false,"exit_code":2,"stdout":"","stderr":"missing input"}`)
	}})
	var result string
	for i := 0; i < repeatBeforeNudge; i++ {
		var err error
		result, err = tool.InvokableRun(ctx, `{"command":"run-tests"}`)
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls != repeatBeforeNudge || !strings.Contains(result, "每次返回的有效结果都一样") {
		t.Fatalf("repeated failures must be observed, not cached or blocked: calls=%d result=%s", calls, result)
	}
}

func TestExecutionRevisionsCaptureUndeclaredWorkspaceWithoutDeclaringDelivery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "final.zip"), []byte("actual bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	d := newDeliveryTracker(root)
	files, partial := executionRevisions(withDelivery(context.Background(), d))
	if partial || len(files) != 1 || files[0].Path != "final.zip" {
		t.Fatalf("missing undeclared archive observation: %+v partial=%v", files, partial)
	}
	if len(d.snapshot()) != 0 {
		t.Fatal("workspace observation fabricated required deliverables")
	}
}

func TestWorkspaceSampleBoundedStableAndExcludesDependencies(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"src", ".git", "node_modules", "__pycache__"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "marker"), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 70; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%02d.txt", i)), []byte("v"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	paths, partial := executionWorkspacePaths(context.Background(), root)
	if !partial || len(paths) != 64 || paths[0] != "file-00.txt" || paths[63] != "file-63.txt" {
		t.Fatalf("unexpected sample: %v %v", paths, partial)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	paths, partial = executionWorkspacePaths(ctx, root)
	if !partial || len(paths) != 0 {
		t.Fatalf("canceled traversal continued: %v %v", paths, partial)
	}
}

func TestUndeclaredWriteReminderDoesNotInventOrRepeatRequirements(t *testing.T) {
	d := newDeliveryTracker(t.TempDir())
	if d.noteUndeclaredWrites(nil) != "" || d.noteUndeclaredWrites([]string{"../escape"}) != "" {
		t.Fatal("spurious reminder")
	}
	if !strings.Contains(d.noteUndeclaredWrites([]string{"temporary.txt"}), "no delivery outputs") {
		t.Fatal("missing reminder")
	}
	if d.noteUndeclaredWrites([]string{"another.txt"}) != "" || len(d.snapshot()) != 0 {
		t.Fatal("repeated reminder or fabricated contract")
	}
	d = newDeliveryTracker(t.TempDir())
	if err := d.declare([]string{"final.zip"}); err != nil {
		t.Fatal(err)
	}
	if d.noteUndeclaredWrites([]string{"temporary.txt"}) != "" {
		t.Fatal("declared outputs ignored")
	}
}

func TestFileWriteAndProcessWritesShareOneDeliveryReminder(t *testing.T) {
	d := newDeliveryTracker(t.TempDir())
	ctx := withDelivery(context.Background(), d)
	tool := EinoTool(retrievalFixture{"file_write", func(context.Context, map[string]any) (string, error) { return "file written", nil }})
	out, err := tool.InvokableRun(ctx, `{"path":"report.txt","content":"data"}`)
	if err != nil || !strings.Contains(out, "no delivery outputs") {
		t.Fatalf("%s %v", out, err)
	}
	if d.noteUndeclaredWrites([]string{"archive.zip"}) != "" {
		t.Fatal("process write repeated reminder")
	}
	if len(d.snapshot()) != 0 {
		t.Fatal("temporary output became a deliverable")
	}
}
