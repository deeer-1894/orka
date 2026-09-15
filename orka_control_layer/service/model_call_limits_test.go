package service

import (
	"context"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/modelprofile"
)

func TestAgentFirstCallHasOutputLimit(t *testing.T) {
	client := llm.NewMock(llm.Response{Content: "ok", FinishReason: "stop"})
	ag, err := BuildEinoAgent(context.Background(), client, "m", "sys", nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(context.Background(), ag, "perform a complex task"); err != nil {
		t.Fatal(err)
	}
	if got := client.Requests[0].MaxTokens; got != 4096 {
		t.Fatalf("first call output cap = %d, want 4096", got)
	}
}

func TestTruncatedCallsCannotExhaustADKRetriesOrFailover(t *testing.T) {
	bad := llm.Response{Content: "incomplete", FinishReason: "length"}
	client := llm.NewMock(bad, bad, bad, bad)
	ag, err := BuildEinoAgent(context.Background(), client, "m", "sys", nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(context.Background(), ag, "hi"); err == nil {
		t.Fatal("truncated output was accepted as completed work")
	}
	if client.Calls() != 2 {
		t.Fatalf("calls = %d; want 2", client.Calls())
	}
}

func TestTruncatedToolBatchNeverExecutes(t *testing.T) {
	for _, rejected := range []llm.Response{
		{FinishReason: "length", ToolCalls: []llm.ToolCall{{ID: "bad", Name: "echo", Arguments: `{"text":"rejected action"}`}}},
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "bad", Name: "echo", Arguments: `{"text":"rejected action"}`}, {ID: "broken", Name: "echo", Arguments: `{"text":"cut off`}}},
	} {
		t.Run(rejected.FinishReason, func(t *testing.T) {
			calls := 0
			client := llm.NewMock(
				rejected,
				llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "good", Name: "echo", Arguments: `{"text":"accepted action"}`}}},
				llm.Response{FinishReason: "stop", Content: "done"},
			)
			ag, err := BuildEinoAgent(context.Background(), client, "m", "sys", []agent.BaseTool{echoTool{calls: &calls}}, 4)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := RunEinoOnce(context.Background(), ag, "hi"); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || client.Calls() != 3 {
				t.Fatalf("executed %d tools in %d calls, want 1 in 3", calls, client.Calls())
			}
			for _, msg := range client.Requests[2].Messages {
				if msg.ToolCallID == "bad" {
					t.Fatal("discarded action entered tool history")
				}
			}
			if client.Requests[2].MaxTokens != 8192 {
				t.Fatalf("later action cap = %d", client.Requests[2].MaxTokens)
			}
		})
	}
}

func TestLengthRetryClearsDiscardedThinking(t *testing.T) {
	resets := 0
	ctx := agent.WithEmit(context.Background(), func(m messages.Message) {
		if m.Type == "stream" && m.Action == "reset" {
			resets++
		}
	})
	client := llm.NewMock(llm.Response{FinishReason: "length"}, llm.Response{FinishReason: "stop", Content: "ok"})
	ag, err := BuildEinoAgent(ctx, client, "m", "sys", nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(ctx, ag, "hi"); err != nil {
		t.Fatal(err)
	}
	if resets != 1 {
		t.Fatalf("reasoning resets=%d want 1", resets)
	}
}

func TestExecutionReasoningPolicyFollowsActualModel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override string
		want     string
	}{
		{"deepseek-v4-pro", "", "low"}, {"deepseek-v4-flash", "", "low"},
		{"glm-5.3", "", "low"}, {"glm-5.3-flash", "", "low"}, {"unknown", "glm-5.3-flash", "low"}, {"glm-5.3-flash", "unknown", ""}, {"glm-5.3-flash-unknown", "", ""}, {"unknown", "glm-5.3", "low"},
		{"glm-5.3", "unknown", ""},
		{"unknown", "", ""}, {"deepseek-v4-pro", "unknown", ""},
		{"unknown", "deepseek-v4-pro", "low"},
	} {
		t.Run(tc.name+"/"+tc.override, func(t *testing.T) {
			mock := llm.NewMock(llm.Response{Content: "ok"})
			m := newAgentModel(mock, tc.name, "test")
			var opts []model.Option
			if tc.override != "" {
				opts = append(opts, model.WithModel(tc.override))
			}
			_, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("one next action")}, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if got := mock.Requests[0].ReasoningEffort; got != tc.want {
				t.Fatalf("effort %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExecutionReasoningPolicyIsolatedAcrossConcurrentOverrides(t *testing.T) {
	mock := llm.NewMock(llm.Response{Content: "ok"})
	m := newAgentModel(mock, "deepseek-v4-pro", "test")
	var wg sync.WaitGroup
	for _, name := range []string{"deepseek-v4-pro", "deepseek-v4-flash", "deepseek-v4-pro-unknown", "unknown"} {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("task")}, model.WithModel(name)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("base model again")}); err != nil {
		t.Fatal(err)
	}
	for _, req := range mock.Requests {
		want := ""
		if req.Model == "deepseek-v4-pro" || req.Model == "deepseek-v4-flash" {
			want = "low"
		}
		if req.ReasoningEffort != want {
			t.Fatalf("model policy leaked: %+v", req)
		}
	}
	if mock.Requests[len(mock.Requests)-1].Model != "deepseek-v4-pro" {
		t.Fatal("model override leaked to base instance")
	}
}

// Replay a reasoning-heavy first action: a valid tool call follows 6k tokens
// of thinking. The old 4k first-call cap repeatedly stopped before any work.
func TestGLMReasoningCanReachFirstToolAction(t *testing.T) {
	calls := 0
	client := &gateScriptClient{respond: func(n int, req llm.Request) llm.Response {
		for _, message := range req.Messages {
			if message.Role == llm.RoleTool {
				return llm.Response{Content: "done", FinishReason: "stop"}
			}
		}
		if req.MaxTokens <= 6000 {
			return llm.Response{Reasoning: "planning the implementation", FinishReason: "length"}
		}
		return gateCall("work", "echo", `{"text":"execute first action"}`)
	}}
	ag, err := BuildEinoAgent(context.Background(), client, "glm-5.3", "sys", []agent.BaseTool{echoTool{calls: &calls}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(context.Background(), ag, "implement a complex project"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("executed actions = %d, want one", calls)
	}
}

func TestFlashFirstActionHasRoomAfterReasoning(t *testing.T) {
	client := llm.NewMock(llm.Response{Content: "next action", FinishReason: "stop"})
	m := newAgentModel(client, "glm-5.3-flash", "test")
	if _, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("implement one small module")}); err != nil {
		t.Fatal(err)
	}
	if got := client.Requests[0].MaxTokens; got != 8192 {
		t.Fatalf("Flash first output cap=%d, want 8192", got)
	}
}

func TestNamedProfileDoesNotGuessReasoningPolicy(t *testing.T) {
	mock := llm.NewMock(llm.Response{Content: "ok", FinishReason: "stop"})
	ctx := modelprofile.WithContext(context.Background(), modelprofile.Snapshot{ProfileID: "work", Model: "glm-5.3", Protocol: modelprofile.OpenAICompatible})
	m := newAgentModel(mock, "glm-5.3", "test")
	if _, err := m.Generate(ctx, []*schema.Message{schema.UserMessage("action")}); err != nil {
		t.Fatal(err)
	}
	if mock.Requests[0].ReasoningEffort != "" || mock.Requests[0].MaxTokens != 4096 {
		t.Fatal("model name invented provider policy", mock.Requests[0])
	}
}

func TestExplicitNamedModelPolicyOverridesLegacyDefaults(t *testing.T) {
	mock := llm.NewMock(llm.Response{Content: "ok", FinishReason: "stop"})
	ctx := modelprofile.WithContext(context.Background(), modelprofile.Snapshot{ProfileID: "work", Model: "custom", Policy: modelprofile.CallPolicy{FirstMaxTokens: 12000, MaxTokens: 16000, ReasoningEffort: "low", TimeoutSeconds: 240}})
	m := newAgentModel(mock, "custom", "test")
	if _, err := m.Generate(ctx, []*schema.Message{schema.UserMessage("action")}); err != nil {
		t.Fatal(err)
	}
	if mock.Requests[0].ReasoningEffort != "low" || mock.Requests[0].MaxTokens != 12000 {
		t.Fatal("explicit policy ignored", mock.Requests[0])
	}
	// A per-call override must not inherit a different model's capabilities.
	if _, err := m.Generate(ctx, []*schema.Message{schema.UserMessage("action")}, model.WithModel("other")); err != nil {
		t.Fatal(err)
	}
	if mock.Requests[1].ReasoningEffort != "" || mock.Requests[1].MaxTokens != 4096 {
		t.Fatal("policy crossed selected model")
	}
}
