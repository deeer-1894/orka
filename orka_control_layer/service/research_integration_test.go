package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// Exercise the production entry, not a builder supplied with a pre-enriched
// context: construction-time guidance and runtime tools must share one session.
func TestChatRunSharesResearchSessionWithToolInvocations(t *testing.T) {
	model := llm.NewMock(
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "fetch-1", Name: "fetch_url", Arguments: `{"url":"https://example.test/docs"}`}}, FinishReason: "tool_calls"},
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "fetch-2", Name: "fetch_url", Arguments: `{"url":"https://example.test/docs"}`}}, FinishReason: "tool_calls"},
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "lookup", Name: "search_evidence", Arguments: `{"query":"checkpoints"}`}}, FinishReason: "tool_calls"},
		llm.Response{Content: "Research complete.", FinishReason: "stop"},
	)
	svc, _ := testService(t, model)
	base := t.TempDir()
	svc.Cfg.Storage.BaseStoragePath = base
	var calls atomic.Int32
	source := retrievalFixture{"fetch_url", func(context.Context, map[string]any) (string, error) {
		calls.Add(1)
		return "URL: https://example.test/docs\nTitle: Recovery\nPersistent checkpoints survive restarts.", nil
	}}
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{source}, nil, nil
	}
	status := svc.Run(context.Background(), ChatRunRequest{Message: "Research checkpoints", UserEmail: "reader"}, func(messages.Message) {})
	if status != db.RunDone {
		t.Fatalf("status = %q, want done", status)
	}
	if model.Calls() != 4 {
		t.Fatalf("model calls = %d, want the four scripted steps", model.Calls())
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("underlying retrieval calls = %d, want 1: runtime tools lost the run's research session", got)
	}
	var lookup string
	for _, m := range model.Requests[len(model.Requests)-1].Messages {
		if m.ToolCallID == "lookup" {
			lookup = m.Content
		}
	}
	var records []evidenceRecord
	if err := json.Unmarshal([]byte(lookup), &records); err != nil {
		t.Fatalf("evidence lookup = %q: %v", lookup, err)
	}
	if len(records) != 1 {
		t.Fatalf("evidence lookup returned %d records, want 1: %s", len(records), lookup)
	}
	if records[0].URL != "https://example.test/docs" || !strings.Contains(records[0].Excerpt, "Persistent checkpoints") {
		t.Fatalf("evidence lost the source or content: %+v", records[0])
	}
	if records[0].Path == "" {
		t.Fatal("evidence has no persisted path")
	}
	body, err := os.ReadFile(filepath.Join(base, "reader", records[0].Path))
	if err != nil || !strings.Contains(string(body), "Persistent checkpoints survive restarts.") {
		t.Fatalf("advertised evidence file = %q, err=%v", body, err)
	}
}

func TestChatRunWithoutMongoKeepsEvidenceSeparateBetweenExecutions(t *testing.T) {
	base := t.TempDir()
	var paths, catalogs []string
	var firstBody, firstCatalog []byte
	for _, body := range []string{"first execution checkpoints", "second execution checkpoints"} {
		model := llm.NewMock(
			llm.Response{ToolCalls: []llm.ToolCall{{ID: "fetch", Name: "fetch_url", Arguments: `{"url":"https://example.test/docs"}`}}, FinishReason: "tool_calls"},
			llm.Response{ToolCalls: []llm.ToolCall{{ID: "lookup", Name: "search_evidence", Arguments: `{"query":"checkpoints"}`}}, FinishReason: "tool_calls"},
			llm.Response{Content: "Research complete.", FinishReason: "stop"},
		)
		svc, _ := testService(t, model) // No Mongo: createRun cannot assign a run record ID.
		svc.Cfg.Storage.BaseStoragePath = base
		source := retrievalFixture{"fetch_url", func(context.Context, map[string]any) (string, error) {
			return "URL: https://example.test/docs\nTitle: Recovery\n" + body, nil
		}}
		svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
			return []agent.BaseTool{source}, nil, nil
		}
		status := svc.Run(context.Background(), ChatRunRequest{Message: "Research checkpoints", UserEmail: "reader"}, func(messages.Message) {})
		if status != db.RunDone || model.Calls() != 3 {
			t.Fatalf("run status=%q model calls=%d, want done/3", status, model.Calls())
		}
		var records []evidenceRecord
		var catalog string
		for _, m := range model.Requests[len(model.Requests)-1].Messages {
			if m.ToolCallID == "lookup" {
				if err := json.Unmarshal([]byte(m.Content), &records); err != nil {
					t.Fatalf("evidence lookup = %q: %v", m.Content, err)
				}
			}
			const marker = "Source catalog: "
			if i := strings.Index(m.Content, marker); i >= 0 {
				if fields := strings.Fields(m.Content[i+len(marker):]); len(fields) > 0 {
					catalog = fields[0]
				}
			}
		}
		if len(records) != 1 || records[0].Path == "" || catalog == "" {
			t.Fatalf("missing evidence/catalog: records=%+v catalog=%q", records, catalog)
		}
		paths = append(paths, filepath.Join(base, "reader", records[0].Path))
		catalogs = append(catalogs, filepath.Join(base, "reader", catalog))
		stored, err := os.ReadFile(paths[len(paths)-1])
		if err != nil || !strings.Contains(string(stored), body) {
			t.Fatalf("this execution's evidence=%q err=%v, want %q", stored, err, body)
		}
		catalogBody, err := os.ReadFile(catalogs[len(catalogs)-1])
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) == 1 {
			firstBody, firstCatalog = stored, catalogBody
		}
	}
	if paths[0] == paths[1] {
		t.Errorf("independent runs advertise the same evidence path: %s", paths[0])
	}
	if catalogs[0] == catalogs[1] {
		t.Errorf("independent runs advertise the same catalog path: %s", catalogs[0])
	}
	afterBody, err := os.ReadFile(paths[0])
	if err != nil || string(afterBody) != string(firstBody) {
		t.Errorf("second execution overwrote the first evidence: %q, err=%v", afterBody, err)
	}
	afterCatalog, err := os.ReadFile(catalogs[0])
	if err != nil || string(afterCatalog) != string(firstCatalog) {
		t.Errorf("second execution overwrote the first catalog: %q, err=%v", afterCatalog, err)
	}
}
