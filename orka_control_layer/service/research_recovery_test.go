package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/pathsafe"
)

func recoverySource(t *testing.T, base, owner, conv string) (*researchSession, string) {
	t.Helper()
	b := newRunBudget(100, 800000, 0)
	b.AddUsage(550000, 0)
	s := newResearchSession(newWorkspaceBackend(base, owner, conv), ".orka_offload/original/evidence", b, 40)
	body := "URL: https://docs.example.test/resolved\nTitle: Original official documentation\n\n" + strings.Repeat("Original source body. ", 400) + "distinctive-final-paragraph"
	out, err := s.invoke(context.Background(), "fetch_url", map[string]any{"url": "https://docs.example.test/redirect"}, func() (string, error) { return body, nil })
	if err != nil || !strings.Contains(out, "Evidence") {
		t.Fatalf("capture failed: %s %v", out, err)
	}
	// Use a fixed historical capture time so replacing it with time.Now during
	// recovery cannot accidentally pass within the same second.
	r := &s.evidence.records[0]
	r.RetrievedAt = "2024-02-03T04:05:06Z"
	root, _ := pathsafe.SessionRoot(base, owner, conv)
	file := "Source: " + r.URL + "\nTool: " + r.Tool + "\nRetrieved: " + r.RetrievedAt + "\n\n" + body
	if err := os.WriteFile(filepath.Join(root, r.Path), []byte(file), 0600); err != nil {
		t.Fatal(err)
	}
	r.persistedHash = sha256.Sum256([]byte(file))
	r.persistedBytes = int64(len(file))
	return s, body
}
func serializedResearchCheckpoint(t *testing.T, s *researchSession) *runCheckpoint {
	t.Helper()
	raw, err := json.Marshal(checkpointFrom(withResearchSession(withBudget(context.Background(), s.budget), s)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Original source body.") || strings.Contains(string(raw), "distinctive-final-paragraph") {
		t.Fatal("checkpoint embeds source body")
	}
	var cp runCheckpoint
	if err = json.Unmarshal(raw, &cp); err != nil {
		t.Fatal(err)
	}
	return &cp
}
func recoverResearch(t *testing.T, base, owner, conv, dir string, cp *runCheckpoint) *researchSession {
	t.Helper()
	b := newRunBudget(100, 800000, 0)
	restoreCheckpoint(cp, b, nil, nil)
	s := newResearchSession(newWorkspaceBackend(base, owner, conv), dir, b, 40)
	s.restoreCheckpoint(context.Background(), cp)
	return s
}
func TestEvidenceRecoveryRoundTripRetainsSourceTimeBodyAndBudget(t *testing.T) {
	base := t.TempDir()
	original, body := recoverySource(t, base, "owner", "A")
	before := original.evidence.search("", 10)[0]
	cp := serializedResearchCheckpoint(t, original)
	for _, dir := range []string{".orka_offload/resumed/evidence", ".orka_offload/resumed-again/evidence"} {
		restored := recoverResearch(t, base, "owner", "A", dir, cp)
		hits := restored.evidence.search("distinctive-final-paragraph", 10)
		if len(hits) != 1 {
			t.Fatalf("saved source not searchable after resume: %+v", hits)
		}
		got := hits[0]
		if got.ID != before.ID || got.URL != before.URL || got.Tool != before.Tool || got.Title != before.Title || got.RetrievedAt != before.RetrievedAt || got.Path != before.Path || got.body != body || got.persistedHash != before.persistedHash {
			t.Fatalf("provenance/body changed: %+v", got)
		}
		if restored.calls != 1 || restored.budget.totalSpentTokens() != 550000 || restored.budget.spentTokens() != 0 {
			t.Fatal("resume reset retrieval allowance or rebilled prior tokens")
		}
		root, _ := pathsafe.SessionRoot(base, "owner", "A")
		file, err := os.ReadFile(filepath.Join(root, got.Path))
		if err != nil {
			t.Fatal(err)
		}
		ctx := withResearchSession(context.Background(), restored)
		if first := restored.evidence.reduceRepeatedRead(ctx, got.Path, string(file)); first != string(file) {
			t.Fatal("first read suppressed")
		}
		if second := restored.evidence.reduceRepeatedRead(ctx, got.Path, string(file)); !strings.Contains(second, "unchanged evidence") {
			t.Fatal("restored content hash unavailable to read tracker")
		}
		cp = serializedResearchCheckpoint(t, restored)
	}
}
func TestEvidenceRecoveryRejectsChangedMissingAndCrossSession(t *testing.T) {
	for _, mode := range []string{"changed", "missing", "cross-session", "cross-owner", "symlink-outside"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			original, _ := recoverySource(t, base, "owner", "A")
			cp := serializedResearchCheckpoint(t, original)
			r := original.evidence.records[0]
			root, _ := pathsafe.SessionRoot(base, "owner", "A")
			owner, conv := "owner", "A"
			switch mode {
			case "changed":
				if err := os.WriteFile(filepath.Join(root, r.Path), []byte("forged changed content"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(filepath.Join(root, r.Path)); err != nil {
					t.Fatal(err)
				}
			case "cross-session", "cross-owner":
				if mode == "cross-session" {
					conv = "B"
				} else {
					owner = "other"
				}
				otherRoot, err := pathsafe.EnsureSession(base, owner, conv)
				if err != nil {
					t.Fatal(err)
				}
				content, err := os.ReadFile(filepath.Join(root, r.Path))
				if err != nil {
					t.Fatal(err)
				}
				dst := filepath.Join(otherRoot, r.Path)
				if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(dst, content, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink-outside":
				src := filepath.Join(root, r.Path)
				content, err := os.ReadFile(src)
				if err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "source.txt")
				if err = os.WriteFile(outside, content, 0600); err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(src); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(outside, src); err != nil {
					t.Fatal(err)
				}
			}
			restored := recoverResearch(t, base, owner, conv, ".orka_offload/resumed/evidence", cp)
			if got := restored.evidence.search("", 10); len(got) != 0 {
				t.Fatalf("unverified source restored: %+v", got)
			}
			out, err := (evidenceSearchTool{restored}).Invoke(context.Background(), nil)
			if err != nil || !strings.Contains(out, "recovery_warnings") || strings.Contains(out, "forged changed content") {
				t.Fatalf("missing explicit recovery degradation: %s %v", out, err)
			}
			if !strings.Contains(restored.status(), "Evidence recovery") {
				t.Fatal("model guidance hides lost evidence")
			}
			again := recoverResearch(t, base, owner, conv, ".orka_offload/again/evidence", serializedResearchCheckpoint(t, restored))
			out, _ = (evidenceSearchTool{again}).Invoke(context.Background(), nil)
			if !strings.Contains(out, "recovery_warnings") {
				t.Fatal("degradation disappeared on second resume")
			}
		})
	}
}
func TestLegacyEvidenceRecoveryDoesNotScanUnreferencedFiles(t *testing.T) {
	base := t.TempDir()
	_, _ = recoverySource(t, base, "owner", "A")
	cp := &runCheckpoint{ResearchCalls: 40, SpentTokens: 599999}
	restored := recoverResearch(t, base, "owner", "A", ".orka_offload/resumed/evidence", cp)
	if got := restored.evidence.search("", 10); len(got) != 0 {
		t.Fatal("unreferenced files promoted to sources")
	}
	out, err := (evidenceSearchTool{restored}).Invoke(context.Background(), nil)
	if err != nil || !strings.Contains(out, "recovery_warnings") {
		t.Fatalf("legacy recovery silently lost evidence: %s %v", out, err)
	}
	remote := false
	out, err = restored.invoke(context.Background(), "fetch_url", map[string]any{"url": "https://docs.example.test/new"}, func() (string, error) { remote = true; return "new", nil })
	if err != nil || remote || out != researchLimitNotice || restored.calls != 40 {
		t.Fatal("recovery reset 40-call allowance")
	}
}
func TestEvidenceRecoveryReferencesAreValidatedIndependently(t *testing.T) {
	for _, mode := range []string{"traversal", "absolute", "bad-hash", "bad-time", "bad-header", "missing-reference"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			source, _ := recoverySource(t, base, "owner", "A")
			cp := serializedResearchCheckpoint(t, source)
			raw, _ := json.Marshal(cp)
			var obj map[string]any
			json.Unmarshal(raw, &obj)
			recovery, ok := obj["evidence"].(map[string]any)
			if !ok {
				t.Fatal("checkpoint contains no evidence references")
			}
			refs, ok := recovery["records"].([]any)
			if !ok || len(refs) != 1 {
				t.Fatal("missing record references")
			}
			ref := refs[0].(map[string]any)
			switch mode {
			case "traversal":
				ref["path"] = "../A/" + source.evidence.records[0].Path
			case "absolute":
				root, _ := pathsafe.SessionRoot(base, "owner", "A")
				ref["path"] = filepath.Join(root, source.evidence.records[0].Path)
			case "bad-hash":
				ref["sha256"] = "not-a-hash"
			case "bad-time":
				ref["retrieved_at"] = "invalid"
			case "bad-header":
				ref["url"] = "https://forged.example.test/"
			case "missing-reference":
				ref["path"] = ""
			}
			raw, _ = json.Marshal(obj)
			if err := json.Unmarshal(raw, cp); err != nil {
				t.Fatal(err)
			}
			restored := recoverResearch(t, base, "owner", "A", ".orka_offload/resumed/evidence", cp)
			if hits := restored.evidence.search("", 10); len(hits) != 0 {
				t.Fatalf("invalid %s reference accepted: %+v", mode, hits)
			}
			out, _ := (evidenceSearchTool{restored}).Invoke(context.Background(), nil)
			if !strings.Contains(out, "recovery_warnings") {
				t.Fatal("invalid reference silently discarded")
			}
		})
	}
}
func TestEvidenceRecoveryIgnoresEditableCatalogAndRetainsOriginalReferences(t *testing.T) {
	base := t.TempDir()
	source, _ := recoverySource(t, base, "owner", "A")
	cp := serializedResearchCheckpoint(t, source)
	before := source.evidence.search("", 10)
	root, _ := pathsafe.SessionRoot(base, "owner", "A")
	if err := os.WriteFile(filepath.Join(root, source.evidence.indexPath), []byte(`[{"url":"https://forged.example.test"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	restored := recoverResearch(t, base, "owner", "A", ".orka_offload/resumed/evidence", cp)
	if got := restored.evidence.search("", 10); !reflect.DeepEqual(got, before) {
		t.Fatal("trusted reference lost or editable catalog trusted")
	}
}

func TestEvidenceRecoveryKeepsValidSourcesAlongsideSameSizeCorruption(t *testing.T) {
	base := t.TempDir()
	original, _ := recoverySource(t, base, "owner", "A")
	original.invoke(context.Background(), "fetch_url", map[string]any{"url": "https://docs.example.test/second"}, func() (string, error) { return "second valid source text", nil })
	cp := serializedResearchCheckpoint(t, original)
	root, _ := pathsafe.SessionRoot(base, "owner", "A")
	path := filepath.Join(root, original.evidence.records[0].Path)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1 // Same length and untouched provenance header; only hash catches it.
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	restored := recoverResearch(t, base, "owner", "A", ".orka_offload/recovered/evidence", cp)
	hits := restored.evidence.search("", 10)
	if len(hits) != 1 || !strings.Contains(hits[0].body, "second valid source") || hits[0].ID != original.evidence.records[1].ID {
		t.Fatalf("corrupt record erased valid sibling: %+v", hits)
	}
	result, err := (evidenceSearchTool{restored}).Invoke(context.Background(), nil)
	if err != nil || !strings.Contains(result, "file content changed since capture") || strings.Contains(result, "Original source body") {
		t.Fatalf("corrupt bytes promoted to evidence: %s %v", result, err)
	}
	cp = serializedResearchCheckpoint(t, restored)
	again := recoverResearch(t, base, "owner", "A", ".orka_offload/again/evidence", cp)
	if hits = again.evidence.search("", 10); len(hits) != 1 {
		t.Fatal("valid source lost during next recovery")
	}
	if again.calls != 2 {
		t.Fatal("invalid source refunded remote allowance")
	}
}

func TestEvidenceRecoveryThroughRunInitialization(t *testing.T) {
	base := t.TempDir()
	source, _ := recoverySource(t, base, "owner", "A")
	cp := serializedResearchCheckpoint(t, source)
	model := llm.NewMock(
		llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "find", Name: "search_evidence", Arguments: `{"query":"distinctive-final-paragraph"}`}}},
		llm.Response{FinishReason: "stop", Content: "Recovered original evidence."},
	)
	svc, _ := testService(t, model)
	svc.Cfg.Storage.BaseStoragePath = base
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) { return nil, nil, nil }
	rr := &runResume{Checkpoint: cp}
	status := svc.Run(context.Background(), ChatRunRequest{Message: "Continue with the saved evidence.", UserEmail: "owner", ConversationID: "A", resumeFrom: rr}, func(messages.Message) {})
	if status != db.RunDone {
		t.Fatalf("resumed run status=%s", status)
	}
	if len(model.Requests) != 2 {
		t.Fatalf("unexpected model calls: %d", len(model.Requests))
	}
	raw, _ := json.Marshal(model.Requests[1].Messages)
	if !strings.Contains(string(raw), "distinctive-final-paragraph") || !strings.Contains(string(raw), "2024-02-03T04:05:06Z") || !strings.Contains(string(raw), "https://docs.example.test/resolved") {
		t.Fatalf("runner did not restore evidence before search: %s", raw)
	}
	if rr.Checkpoint.ResearchCalls != 1 || rr.Checkpoint.Evidence == nil || len(rr.Checkpoint.Evidence.Records) != 1 {
		t.Fatal("successor checkpoint lost recovered references or allowance")
	}
}

func TestEvidenceRecoveryDoesNotPopulateRequestCache(t *testing.T) {
	base := t.TempDir()
	original, _ := recoverySource(t, base, "owner", "A")
	restored := recoverResearch(t, base, "owner", "A", ".orka_offload/resumed/evidence", serializedResearchCheckpoint(t, original))
	if len(restored.cache) != 0 {
		t.Fatal("evidence references must not be promoted to request cache entries")
	}
	calls := 0
	for _, args := range []map[string]any{
		{"url": "https://docs.example.test/redirect"},
		{"url": "https://docs.example.test/redirect", "query": "different-query-key"},
	} {
		out, err := restored.invoke(context.Background(), "fetch_url", args, func() (string, error) { calls++; return "fresh observation", nil })
		if err != nil || strings.Contains(out, "cached evidence") {
			t.Fatalf("false cache hit: %s %v", out, err)
		}
	}
	if calls != 2 || restored.calls != 3 {
		t.Fatalf("network calls=%d, allowance=%d", calls, restored.calls)
	}
}

func TestEvidenceRecoveryCheckpointConcurrentCapture(t *testing.T) {
	base := t.TempDir()
	original, _ := recoverySource(t, base, "owner", "A")
	restored := recoverResearch(t, base, "owner", "A", ".orka_offload/resumed/evidence", serializedResearchCheckpoint(t, original))
	ctx := withResearchSession(withBudget(context.Background(), restored.budget), restored)
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 100; i++ {
			_ = checkpointFrom(ctx)
			_ = restored.status()
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 20; i++ {
			_, _ = restored.invoke(ctx, "fetch_url", map[string]any{"url": "https://example.test/page", "query": i}, func() (string, error) { return "concurrent capture", nil })
		}
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-timer.C:
			t.Fatal("checkpoint/capture lock order deadlocked")
		}
	}
	cp := checkpointFrom(ctx)
	if cp.ResearchCalls != 21 || len(cp.Evidence.Records) != 21 {
		t.Fatalf("incomplete final snapshot: calls=%d records=%d", cp.ResearchCalls, len(cp.Evidence.Records))
	}
}

func TestEvidenceRecoveryRejectsReplacedWorkspaceRoot(t *testing.T) {
	base := t.TempDir()
	original, _ := recoverySource(t, base, "owner", "A")
	cp := serializedResearchCheckpoint(t, original)
	backend := original.evidence.backend.(*workspaceBackend)
	moved := backend.root + "-moved"
	if err := os.Rename(backend.root, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, backend.root); err != nil {
		t.Fatal(err)
	}
	restored := newResearchSession(backend, ".orka_offload/resumed/evidence", original.budget, 40)
	restored.restoreCheckpoint(context.Background(), cp)
	if len(restored.evidence.search("", 10)) != 0 || len(restored.evidence.recoveryProblems()) == 0 {
		t.Fatal("replaced workspace root silently accepted")
	}
}
