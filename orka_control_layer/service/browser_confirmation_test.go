package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

func TestBrowserConfirmationUsesOperation(t *testing.T) {
	for _, action := range []string{"snapshot", "wait", "screenshot"} {
		if needsConfirm("browser", map[string]any{"action": action}) {
			t.Errorf("read action %s required approval", action)
		}
	}
	for _, action := range []string{"open", "click", "fill", "select", "press", "scroll", "evaluate", "download", "unknown"} {
		if !needsConfirm("browser", map[string]any{"action": action}) {
			t.Errorf("mutating action %s bypassed approval", action)
		}
	}
	for _, action := range []string{"fill", "evaluate"} {
		s := summarizeAction("browser", map[string]any{"action": action, "text": "private-password", "expression": "private-script"})
		if strings.Contains(s, "private") || s == "browser" {
			t.Fatalf("unsafe/uninformative confirmation: %s", s)
		}
	}
}

type browserCaptureTool struct{ received string }

func (*browserCaptureTool) Name() string        { return "browser" }
func (*browserCaptureTool) Description() string { return "fixture browser" }
func (*browserCaptureTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}, "selector": map[string]any{"type": "string"}}}
}
func (t *browserCaptureTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	t.received, _ = args["text"].(string)
	return `{"ok":true,"snapshot":{"text":"submitted"}}`, nil
}
func TestBrowserRunRedactsDisplayWithoutChangingExecutedInput(t *testing.T) {
	browser := &browserCaptureTool{}
	model := llm.NewMock(gateCall("fill", "browser", `{"action":"fill","selector":"input","text":"private-password"}`), llm.Response{Content: "finished", FinishReason: "stop"})
	svc, _ := testService(t, model)
	svc.Cfg.Storage.BaseStoragePath = t.TempDir()
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{browser}, nil, nil
	}
	col := &collector{}
	svc.Run(context.Background(), ChatRunRequest{UserEmail: "fixture", ConversationID: "browser-redaction", Message: "fill field"}, col.sink)
	if browser.received != "private-password" {
		t.Fatalf("live input mutated: %q", browser.received)
	}
	events := col.byType(messages.EventTool)
	if len(events) == 0 {
		t.Fatal("no tool display event")
	}
	encoded, _ := json.Marshal(events)
	if strings.Contains(string(encoded), "private-password") || !strings.Contains(string(encoded), "submitted") {
		t.Fatalf("display leaked arguments or dropped observation: %s", encoded)
	}
}
func TestBrowserJournalRedactsCopyWithoutChangingReplayInput(t *testing.T) {
	root := t.TempDir()
	original := schema.AssistantMessage("", []schema.ToolCall{{ID: "script", Function: schema.FunctionCall{Name: "browser", Arguments: `{"action":"evaluate","expression":"private-script"}`}}})
	j := newRunJournal(root, "browser-journal", nil)
	j.append(original)
	if !j.flush() {
		t.Fatal("journal write failed")
	}
	data, err := os.ReadFile(j.path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-script") {
		t.Fatal("journal duplicated expression")
	}
	if !strings.Contains(original.ToolCalls[0].Function.Arguments, "private-script") {
		t.Fatal("journal redaction mutated live arguments")
	}
}

func TestBrowserRunConfirmationIsOperationSpecific(t *testing.T) {
	for _, action := range []string{"snapshot", "fill"} {
		t.Run(action, func(t *testing.T) {
			browser := &browserCaptureTool{}
			args := `{"action":"snapshot"}`
			if action == "fill" {
				args = `{"action":"fill","selector":"input","text":"private-password"}`
			}
			model := llm.NewMock(gateCall("operation", "browser", args), llm.Response{Content: "finished", FinishReason: "stop"})
			svc, _ := testService(t, model)
			svc.Cfg.Storage.BaseStoragePath = t.TempDir()
			svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
				return []agent.BaseTool{browser}, nil, nil
			}
			col := &collector{}
			status := svc.Run(context.Background(), ChatRunRequest{UserEmail: "fixture", ConversationID: "browser-confirm", Message: "operate browser", ConfirmRisky: true}, col.sink)
			confirms := col.byType(messages.EventConfirm)
			if action == "snapshot" {
				if status != "done" || len(confirms) != 0 {
					t.Fatalf("read was blocked: %s %+v", status, confirms)
				}
			} else {
				if status != "paused" || len(confirms) != 1 || browser.received != "" {
					t.Fatalf("mutation ran without approval: %s %+v", status, confirms)
				}
				encoded, _ := json.Marshal(confirms)
				if strings.Contains(string(encoded), "private-password") {
					t.Fatal("approval leaked input")
				}
				if !svc.ResumeConfirm(context.Background(), "browser-confirm", true, false, col.sink) || browser.received != "private-password" {
					t.Fatal("approval lost the private execution input")
				}
			}
		})
	}
}
