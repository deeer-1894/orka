package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type retrievalFixture struct {
	name string
	call func(context.Context, map[string]any) (string, error)
}

func (f retrievalFixture) Name() string           { return f.name }
func (f retrievalFixture) Description() string    { return "Retrieve a test document." }
func (f retrievalFixture) Schema() map[string]any { return nil }
func (f retrievalFixture) Invoke(ctx context.Context, args map[string]any) (string, error) {
	return f.call(ctx, args)
}

func TestRetrievalCacheDoesNotCrossIndependentRuns(t *testing.T) {
	for _, content := range []string{"first user's response", "second user's response"} {
		tool := EinoTool(retrievalFixture{"fetch_url", func(context.Context, map[string]any) (string, error) {
			return content, nil
		}})
		got, err := tool.InvokableRun(context.Background(), `{"url":"https://example.test/cache-isolation"}`)
		if err != nil || got != content {
			t.Fatalf("independent invocation = %q, %v; want %q", got, err, content)
		}
	}
}

func TestResearchSessionCachesNormalizedRequestsAndPersistsEvidence(t *testing.T) {
	base := t.TempDir()
	s := newResearchSession(newWorkspaceBackend(base, "reader"), ".orka_offload/run-test/evidence", nil, 10)
	ctx := withResearchSession(context.Background(), s)
	calls := 0
	tool := EinoTool(retrievalFixture{"fetch_url", func(context.Context, map[string]any) (string, error) {
		calls++
		return "URL: https://example.test/docs\nTitle: Recovery\n\nPersistent checkpoints survive restarts.", nil
	}})
	first, err := tool.InvokableRun(ctx, `{"url":"https://example.test/docs#first"}`)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tool.InvokableRun(ctx, `{ "url": "https://example.test/docs#second" }`)
	if err != nil || calls != 1 || !strings.Contains(second, "cached") {
		t.Fatalf("calls=%d second=%q err=%v", calls, second, err)
	}
	if !strings.Contains(first, ".orka_offload/run-test/evidence/") {
		t.Fatal("missing durable receipt", first)
	}
	got, err := (evidenceSearchTool{s}).Invoke(ctx, map[string]any{"query": "checkpoints"})
	if err != nil || !strings.Contains(got, "Persistent checkpoints") || !strings.Contains(got, "https://example.test/docs") {
		t.Fatalf("search=%q err=%v", got, err)
	}
	files, _ := filepath.Glob(filepath.Join(base, "reader", ".orka_offload/run-test/evidence/*.txt"))
	if len(files) != 1 {
		t.Fatalf("evidence files=%v", files)
	}
	body, _ := os.ReadFile(files[0])
	if !strings.Contains(string(body), "Persistent checkpoints") {
		t.Fatal("lost body")
	}
}

func TestResearchSessionRetriesErrorsAndReservesDeliveryBudget(t *testing.T) {
	budget := newRunBudget(20, 1000, 0)
	s := newResearchSession(nil, "", budget, 10)
	ctx := withResearchSession(context.Background(), s)
	calls := 0
	tool := EinoTool(retrievalFixture{"fetch_url", func(context.Context, map[string]any) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("HTTP 404")
		}
		return "a valid page", nil
	}})
	_, _ = tool.InvokableRun(ctx, `{"url":"https://example.test/retry"}`)
	got, _ := tool.InvokableRun(ctx, `{"url":"https://example.test/retry"}`)
	if calls != 2 || !strings.Contains(got, "a valid page") {
		t.Fatalf("failed response cached: %d %q", calls, got)
	}
	budget.AddUsage(500, 0)
	got, _ = tool.InvokableRun(ctx, `{"url":"https://example.test/new"}`)
	if calls != 2 || !strings.Contains(got, "retrieval budget") {
		t.Fatalf("retrieval not limited: %d %q", calls, got)
	}
	got, _ = tool.InvokableRun(ctx, `{"url":"https://example.test/retry"}`)
	if !strings.Contains(got, "a valid page") {
		t.Fatal("cache inaccessible after limit", got)
	}
	shell := EinoTool(retrievalFixture{"shell", func(context.Context, map[string]any) (string, error) { return "delivery works", nil }})
	got, _ = shell.InvokableRun(ctx, `{}`)
	if got != "delivery works" {
		t.Fatal("delivery blocked", got)
	}
}

func TestResearchSessionCoalescesConcurrentCalls(t *testing.T) {
	s := newResearchSession(nil, "", nil, 1)
	ctx := withResearchSession(context.Background(), s)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	tool := EinoTool(retrievalFixture{"fetch_url", func(context.Context, map[string]any) (string, error) {
		calls.Add(1)
		close(entered)
		<-release
		return "shared response", nil
	}})
	done := make(chan string, 1)
	go func() { r, _ := tool.InvokableRun(ctx, `{"url":"https://example.test/concurrent"}`); done <- r }()
	<-entered
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := tool.InvokableRun(canceled, `{"url":"https://example.test/concurrent"}`); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	other := make(chan string, 1)
	go func() { r, _ := tool.InvokableRun(ctx, `{"url":"https://example.test/concurrent"}`); other <- r }()
	close(release)
	if a, b := <-done, <-other; !strings.Contains(a, "shared response") || !strings.Contains(b, "shared response") || calls.Load() != 1 {
		t.Fatalf("calls=%d results=%q,%q", calls.Load(), a, b)
	}
}

func TestResearchGuidanceSurvivesHistoryReplacement(t *testing.T) {
	b := newRunBudget(20, 1000, 0)
	s := newResearchSession(nil, "", b, 10)
	p := &planTracker{}
	p.record([]messages.PlanStep{{Title: "Research", Status: "done"}, {Title: "Generate and verify data", Status: "active"}})
	ctx := withPlanTracker(withResearchSession(context.Background(), s), p)
	g := newResearchGuidance(ctx)
	b.AddUsage(500, 0)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.SystemMessage("instruction"), schema.UserMessage("summary replaced the full history")}, ToolInfos: infos("fetch_url", "web_search", "search_evidence", "file_write", "shell")}
	for i := 0; i < 2; i++ {
		_, state, _ = g.BeforeModelRewriteState(ctx, state, nil)
		if len(state.Messages) != 3 {
			t.Fatalf("guidance accumulated: %d", len(state.Messages))
		}
		last := state.Messages[len(state.Messages)-1].Content
		if !strings.Contains(last, "Generate and verify data") || !strings.Contains(last, "retrieval budget") {
			t.Fatal("lost live state", last)
		}
		visible := names(state.ToolInfos)
		if visible["fetch_url"] || !visible["shell"] || !visible["search_evidence"] {
			t.Fatal("wrong remaining tools", visible)
		}
	}
}

func TestGateShowsDocumentationEvidenceAndDeepTask(t *testing.T) {
	g := newToolGate()
	g.remember(infos("discover_docs", "read_section", "search_evidence", "task"))
	got := names(g.visible())
	for _, name := range []string{"discover_docs", "read_section", "search_evidence", "task"} {
		if !got[name] {
			t.Errorf("hidden core tool %s", name)
		}
	}
}

func TestEvidenceIDsResolveAndCatalogSnapshotsStayReadable(t *testing.T) {
	base := t.TempDir()
	s := newResearchSession(newWorkspaceBackend(base, "reader"), ".orka_offload/run-test/evidence", nil, 10)
	ctx := withResearchSession(context.Background(), s)
	tool := EinoTool(retrievalFixture{"fetch_url", func(_ context.Context, args map[string]any) (string, error) {
		return "URL: " + args["url"].(string) + "\nTitle: durable evidence\nbody", nil
	}})
	_, _ = tool.InvokableRun(ctx, `{"url":"https://example.test/one"}`)
	raw, _ := (evidenceSearchTool{s}).Invoke(ctx, map[string]any{})
	var records []evidenceRecord
	if err := json.Unmarshal([]byte(raw), &records); err != nil || len(records) != 1 {
		t.Fatalf("records %s: %v", raw, err)
	}
	byID, _ := (evidenceSearchTool{s}).Invoke(ctx, map[string]any{"query": records[0].ID})
	if !strings.Contains(byID, "https://example.test/one") {
		t.Errorf("ID lookup=%s", byID)
	}
	originalPath := s.evidence.indexPath
	original, _ := os.ReadFile(filepath.Join(base, "reader", originalPath))
	_, _ = tool.InvokableRun(ctx, `{"url":"https://example.test/two"}`)
	after, _ := os.ReadFile(filepath.Join(base, "reader", originalPath))
	if string(original) != string(after) {
		t.Error("previously advertised catalog was overwritten")
	}
	latest, err := os.ReadFile(filepath.Join(base, "reader", s.evidence.indexPath))
	if err != nil || !strings.Contains(string(latest), "https://example.test/two") {
		t.Fatalf("latest catalog=%s: %v", latest, err)
	}
}

func TestResearchGuidanceDoesNotContradictFinalBudgetNotice(t *testing.T) {
	b := newRunBudget(20, 1000, 0)
	b.AddUsage(1000, 0)
	b.observe(nil)
	s := newResearchSession(nil, "", b, 10)
	ctx := withResearchSession(context.Background(), s)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{budgetNotice("tokens")}}
	_, state, _ = newResearchGuidance(ctx).BeforeModelRewriteState(ctx, state, nil)
	if len(state.Messages) != 1 || strings.Contains(state.Messages[len(state.Messages)-1].Content, "tools remain available") {
		t.Fatal("contradictory final guidance", state.Messages)
	}
}

func TestDocumentationToolsStayInWebScope(t *testing.T) {
	for _, name := range []string{"discover_docs", "read_section"} {
		if groupForName(name) != "web" {
			t.Errorf("%s excluded from web scope", name)
		}
	}
}

func TestResearchSessionWiredThroughEinoRunner(t *testing.T) {
	s := newResearchSession(newWorkspaceBackend(t.TempDir(), "reader"), ".orka_offload/integration/evidence", nil, 10)
	ctx := withToolGate(withResearchSession(context.Background(), s), newToolGate())
	calls := 0
	source := retrievalFixture{"fetch_url", func(context.Context, map[string]any) (string, error) {
		calls++
		return "URL: https://example.test/docs\nTitle: durable source\nKeep checkpoints on disk", nil
	}}
	model := llm.NewMock(
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "one", Name: "fetch_url", Arguments: `{"url":"https://example.test/docs"}`}}},
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "two", Name: "search_evidence", Arguments: `{"query":"checkpoints"}`}}},
		llm.Response{Content: "done"},
	)
	ag, err := BuildEinoAgent(ctx, model, "m", "execute the request", []agent.BaseTool{source, evidenceSearchTool{s}}, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(ctx, ag, "research and deliver"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(model.Requests) != 3 {
		t.Fatalf("calls=%d requests=%d", calls, len(model.Requests))
	}
	raw, _ := json.Marshal(model.Requests[len(model.Requests)-1])
	if !strings.Contains(string(raw), "checkpoints") || !strings.Contains(string(raw), "Source catalog") {
		t.Fatal("runner did not carry durable evidence and live state")
	}
}

func TestEvidenceKeepsResolvedSourceMetadata(t *testing.T) {
	s := newResearchSession(nil, "", nil, 2)
	ctx := withResearchSession(context.Background(), s)
	tool := EinoTool(retrievalFixture{"read_section", func(context.Context, map[string]any) (string, error) {
		return "URL: https://example.test/current\nTitle: Actual document\n\nA relevant section.", nil
	}})
	_, _ = tool.InvokableRun(ctx, `{"url":"https://example.test/old","query":"section"}`)
	raw, _ := (evidenceSearchTool{s}).Invoke(ctx, map[string]any{})
	var records []evidenceRecord
	_ = json.Unmarshal([]byte(raw), &records)
	if len(records) != 1 || records[0].URL != "https://example.test/current" || records[0].Title != "Actual document" {
		t.Fatalf("source metadata=%s", raw)
	}
}
