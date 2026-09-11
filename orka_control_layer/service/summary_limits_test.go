package service

import (
	"context"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/llm"
	"testing"
)

func TestSummaryBoundsOutputAndPreservesHistoryOnTruncation(t *testing.T) {
	for _, finish := range []string{"stop", "length"} {
		t.Run(finish, func(t *testing.T) {
			client := llm.NewMock(llm.Response{Content: "Completed data. Pending report and ZIP.", FinishReason: finish})
			mw := summarizationHandlers(context.Background(), client, "mini")[0]
			state := &adk.ChatModelAgentState{}
			for i := 0; i < 60; i++ {
				state.Messages = append(state.Messages, schema.UserMessage("Keep the report and ZIP requirement."), schema.AssistantMessage("work", nil))
			}
			original := len(state.Messages)
			_, got, err := mw.BeforeModelRewriteState(context.Background(), state, &adk.ModelContext{})
			if err != nil {
				t.Fatal(err)
			}
			if client.Calls() != 1 {
				t.Fatalf("summary calls = %d", client.Calls())
			}
			if limit := client.Requests[0].MaxTokens; limit <= 0 || limit > 8192 {
				t.Errorf("unbounded summary: %d", limit)
			}
			if finish == "length" && len(got.Messages) != original {
				t.Fatal("truncated summary replaced original requirements")
			}
			if finish == "stop" && len(got.Messages) >= original {
				t.Fatal("valid summary was not applied")
			}
		})
	}
}
