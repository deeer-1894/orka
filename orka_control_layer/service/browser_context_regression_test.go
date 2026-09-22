package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

func TestBrowserResearchClearsConsumedPageObservations(t *testing.T) {
	ctx := withExecutionPolicy(agent.WithMeta(context.Background(), messages.Meta{
		UserEmail: "reader@test.com", ConversationID: "browser-research",
	}), executionPolicy{Mode: executionBrowser, StrictSources: true, SourceVerificationOnly: true})
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("Read five articles")}}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("call-%d", i)
		call := schema.AssistantMessage("", []schema.ToolCall{{
			ID: id, Function: schema.FunctionCall{Name: "browser", Arguments: fmt.Sprintf(`{"action":"open","url":"https://example.org/%d"}`, i)},
		}})
		state.Messages = append(state.Messages, call, &schema.Message{
			Role: schema.Tool, ToolCallID: id, ToolName: "browser",
			Content: fmt.Sprintf(`{"ok":true,"url":"https://example.org/%d","snapshot":{"text":"%s"}}`, i, strings.Repeat("article text ", 1800)),
		})
	}
	before := historyTokens(state.Messages)
	for _, mw := range contextHandlers(ctx, t.TempDir(), "reader@test.com", "orka", []agent.BaseTool{namedTool{"browser"}}, nil) {
		_, next, err := mw.BeforeModelRewriteState(ctx, state, nil)
		if err != nil {
			t.Fatal(err)
		}
		state = next
	}
	after := historyTokens(state.Messages)
	if after >= before/2 {
		t.Fatalf("consumed browser pages remained in context: %d -> %d tokens", before, after)
	}
	if !strings.Contains(state.Messages[len(state.Messages)-1].Content, "article text") {
		t.Fatal("latest browser result must remain intact for the next model decision")
	}
}
