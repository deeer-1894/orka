package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvidenceCatalogStaysBelowToolPreviewForFortySources(t *testing.T) {
	base := t.TempDir()
	s := newResearchSession(newWorkspaceBackend(base, "reader"), ".orka_offload/run-test/evidence", nil, 40)
	for i := 0; i < 40; i++ {
		s.evidence.capture(context.Background(), fmt.Sprint(i), "fetch_url", map[string]any{"url": fmt.Sprintf("https://example.test/docs/source-%02d", i)}, "Title: Checkpoint persistence\n\n"+strings.Repeat("A lengthy readable source body. ", 70))
	}
	raw, err := os.ReadFile(filepath.Join(base, "reader", s.evidence.indexPath))
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(string(raw))) > 12000 {
		t.Fatalf("catalog requires offloading: %d chars", len([]rune(string(raw))))
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != 40 {
		t.Fatalf("catalog lost sources: %d %v", len(rows), err)
	}
	for _, r := range rows {
		if r["path"] == nil || r["url"] == nil || r["id"] == nil {
			t.Fatal("lost provenance", r)
		}
		if r["excerpt"] != nil {
			t.Fatal("body preview duplicated into source inventory")
		}
	}
}

func TestRepeatedEvidenceReadsKeepFreshFileSemanticsAndBoundContext(t *testing.T) {
	base := t.TempDir()
	s := newResearchSession(newWorkspaceBackend(base, "reader"), ".orka_offload/run-test/evidence", nil, 40)
	body := strings.Repeat("Checkpoint persistence. ", 1000)
	s.evidence.capture(context.Background(), "one", "fetch_url", map[string]any{"url": "https://example.test/recovery"}, body)
	path := s.evidence.records[0].Path
	persisted, err := os.ReadFile(filepath.Join(base, "reader", path))
	if err != nil {
		t.Fatal(err)
	}
	body = string(persisted)
	ctx := withResearchSession(context.Background(), s)
	calls := 0
	fileTool := EinoTool(retrievalFixture{"file_read", func(context.Context, map[string]any) (string, error) { calls++; return body, nil }})
	args, _ := json.Marshal(map[string]any{"path": path})
	first, err := fileTool.InvokableRun(ctx, string(args))
	if err != nil || first != body {
		t.Fatal("first full read lost", err)
	}
	repeat, err := fileTool.InvokableRun(ctx, string(args))
	if err != nil || len([]rune(repeat)) > 1600 || !strings.Contains(repeat, "search_evidence") {
		t.Fatalf("repeated evidence floods context: %d chars, %v", len([]rune(repeat)), err)
	}
	if calls != 2 {
		t.Fatal("bypassed actual file permission/freshness checks", calls)
	}
	for _, alias := range []string{"file", "filename", "file_path", "filepath"} {
		aliasArgs, _ := json.Marshal(map[string]any{alias: "  " + path + "  "})
		got, _ := fileTool.InvokableRun(ctx, string(aliasArgs))
		if len([]rune(got)) > 1600 || !strings.Contains(got, "search_evidence") {
			t.Errorf("alias %s bypasses compaction: %d chars", alias, len([]rune(got)))
		}
	}
	body = "Changed content must stay visible."
	changed, _ := fileTool.InvokableRun(ctx, string(args))
	if changed != body {
		t.Fatal("hid changed file", changed)
	}
	body = strings.Repeat("Generated report content ", 1000)
	for i := 0; i < 2; i++ {
		got, _ := fileTool.InvokableRun(ctx, `{"path":"delivery/report.md"}`)
		if got != body {
			t.Fatal("altered delivery verification")
		}
	}
	result, _ := (evidenceSearchTool{s}).Invoke(ctx, map[string]any{"query": "Checkpoint"})
	if !strings.Contains(result, "Checkpoint persistence") {
		t.Fatal("source lookup lost evidence")
	}
}
