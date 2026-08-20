package service

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// echoTool is a minimal BaseTool used by the eino end-to-end test: it counts
// invocations and echoes back the "text" arg.
type echoTool struct{ calls *int }

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "Echo back the provided text." }
func (echoTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
	}
}
func (e echoTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	if e.calls != nil {
		*e.calls++
	}
	s, _ := args["text"].(string)
	return s, nil
}

// Phase-1 gate: drive eino's REAL ChatModelAgent + Runner end-to-end through our
// adapters (llm.EinoModel + einoTool) using the mock LLM. Proves the full stack:
// eino runner → EinoModel.Generate → (scripted tool call) → eino ToolsNode →
// einoTool.InvokableRun → our BaseTool.Invoke → result fed back → final answer.
func TestEinoAgentDrivesAdaptersEndToEnd(t *testing.T) {
	calls := 0
	echo := echoTool{calls: &calls}

	mock := llm.NewMock(
		// 1) model asks to call our "echo" tool
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "1", Name: "echo", Arguments: `{"text":"hi"}`}},
			FinishReason: "tool_calls",
		},
		// 2) model produces the final answer
		llm.Response{Content: "all done", FinishReason: "stop"},
	)

	ctx := context.Background()
	ag, err := BuildEinoAgent(ctx, mock, "m", "You are a helpful assistant.", []agent.BaseTool{echo}, 8)
	if err != nil {
		t.Fatalf("build eino agent: %v", err)
	}

	final, err := RunEinoOnce(ctx, ag, "please echo hi")
	if err != nil {
		t.Fatalf("run eino agent: %v", err)
	}
	if final != "all done" {
		t.Fatalf("final answer = %q, want %q", final, "all done")
	}
	if calls != 1 {
		t.Fatalf("echo tool invoked %d times via eino ToolsNode, want 1", calls)
	}
}

func TestUsableAssistantOutputRejectsThinkingOnlyShapes(t *testing.T) {
	tests := []struct {
		name string
		msg  *schema.Message
		want bool
	}{
		{name: "nil", msg: nil, want: false},
		{name: "empty", msg: schema.AssistantMessage("", nil), want: false},
		{name: "closing tag only", msg: schema.AssistantMessage("</think>", nil), want: false},
		{name: "think block only", msg: schema.AssistantMessage("<think>internal</think>", nil), want: false},
		{name: "unfinished think block", msg: schema.AssistantMessage("<think>internal", nil), want: false},
		{name: "final after think block", msg: schema.AssistantMessage("<think>internal</think>final", nil), want: true},
		{name: "normal final", msg: schema.AssistantMessage("final", nil), want: true},
		{name: "tool call", msg: &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "1"}}}, want: true},
		{
			name: "reasoning fallback",
			msg: &schema.Message{
				Role:             schema.Assistant,
				Content:          "internal reasoning",
				ReasoningContent: "internal reasoning",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := usableAssistantOutput(tt.msg); got != tt.want {
				t.Fatalf("usableAssistantOutput() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEinoAgentRetriesReasoningOnlyResponse(t *testing.T) {
	mock := llm.NewMock(
		llm.Response{
			Content:      "internal reasoning",
			Reasoning:    "internal reasoning",
			FinishReason: "stop",
		},
		llm.Response{Content: "最终答案", FinishReason: "stop"},
	)
	ctx := context.Background()
	ag, err := BuildEinoAgent(ctx, mock, "m", "You are a helpful assistant.", nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	final, err := RunEinoOnce(ctx, ag, "answer the question")
	if err != nil {
		t.Fatal(err)
	}
	if final != "最终答案" {
		t.Fatalf("final = %q, want 最终答案", final)
	}
	if mock.Calls() != 2 {
		t.Fatalf("model calls = %d, want 2", mock.Calls())
	}
	lastReq := mock.Requests[1]
	if len(lastReq.Messages) == 0 || lastReq.Messages[len(lastReq.Messages)-1].Content != emptyModelRetryPrompt {
		t.Fatalf("retry request did not include correction prompt: %#v", lastReq.Messages)
	}
}

func TestStreamEinoRunContinuesAfterEmptyResponseRetry(t *testing.T) {
	mock := llm.NewMock(
		llm.Response{Reasoning: "internal reasoning", FinishReason: "stop"},
		llm.Response{Content: "可见最终答案", FinishReason: "stop"},
	)
	ctx := context.Background()
	ag, err := BuildEinoAgent(ctx, mock, "m", "You are a helpful assistant.", nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	rc := &agent.RunContext{
		Ctx:      ctx,
		Vars:     map[string]any{},
		Messages: []messages.Message{messages.Chat(messages.RoleUser, "answer", messages.Meta{})},
		Meta:     messages.Meta{},
	}
	var assistantParts []string
	err = StreamEinoRun(ctx, rc, ag, func(m messages.Message) {
		if m.Type == messages.EventChat && m.Role == messages.RoleAssistant {
			assistantParts = append(assistantParts, m.Content)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(assistantParts, ""); got != "可见最终答案" {
		t.Fatalf("assistant reply = %q, want 可见最终答案", got)
	}
	if got := middlewares.Final(rc); got != "可见最终答案" {
		t.Fatalf("run final = %q, want 可见最终答案", got)
	}
	if mock.Calls() != 2 {
		t.Fatalf("model calls = %d, want 2", mock.Calls())
	}
}

type staticResultTool struct {
	name   string
	result string
	calls  int
}

func (t *staticResultTool) Name() string        { return t.name }
func (t *staticResultTool) Description() string { return t.name }
func (t *staticResultTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *staticResultTool) Invoke(_ context.Context, _ map[string]any) (string, error) {
	t.calls++
	return t.result, nil
}

func TestAnalysisContinuationLocksPrefixAndPublishes(t *testing.T) {
	const prefix = "密封数据表"
	const analysisText = "【核心洞察】\n- 销量下降。"
	query := &staticResultTool{
		name: "sales_query_answer",
		result: `{"status":"ok","answer":"密封数据表","metadata":{"response_mode":"grounded_analysis"},` +
			`"analysis":{"query_id":"sales-20260818-120000-abcdef","fact_ledger":[{"fact_id":"f1","value":1}]}}`,
	}
	publish := &staticResultTool{
		name:   "sales_query_publish",
		result: `{"analysis_text":"【核心洞察】\n- 销量下降。"}`,
	}
	mock := llm.NewMock(
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "q1", Name: query.name, Arguments: `{}`}},
			FinishReason: "tool_calls",
		},
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "p1", Name: publish.name, Arguments: `{"query_id":"sales-20260818-120000-abcdef","narrative":{}}`}},
			FinishReason: "tool_calls",
		},
	)
	ctx := context.Background()
	ag, err := BuildEinoAgent(ctx, mock, "m", "publish grounded analysis", []agent.BaseTool{query, publish}, 8)
	if err != nil {
		t.Fatal(err)
	}
	rc := &agent.RunContext{
		Ctx:      ctx,
		Vars:     map[string]any{},
		Messages: []messages.Message{messages.Chat(messages.RoleUser, "analyze", messages.Meta{})},
		Meta:     messages.Meta{},
	}
	var assistantParts []string
	err = StreamEinoRun(ctx, rc, ag, func(m messages.Message) {
		if m.Type == messages.EventChat && m.Role == messages.RoleAssistant {
			assistantParts = append(assistantParts, m.Content)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if query.calls != 1 || publish.calls != 1 {
		t.Fatalf("tool calls: query=%d publish=%d", query.calls, publish.calls)
		if mock.Calls() != 2 {
			t.Fatalf("model calls = %d, want query + publish only", mock.Calls())
		}
	}
	want := prefix + "\n\n" + analysisText
	if got := strings.Join(assistantParts, "\n\n"); got != want {
		t.Fatalf("assistant reply = %q, want %q", got, want)
	}
	if got := middlewares.Final(rc); got != want {
		t.Fatalf("run final = %q, want %q", got, want)
	}
}

func TestReportPublishURLIsByteSealed(t *testing.T) {
	const answer = "报告已生成：http://127.0.0.1:8000/reports/report-1.html?token=a%2Fb"
	report := &staticResultTool{
		name:   "sales_report_generate",
		result: `{"status":"ok","answer":"` + answer + `"}`,
	}
	mock := llm.NewMock(llm.Response{
		ToolCalls:    []llm.ToolCall{{ID: "r1", Name: report.name, Arguments: `{"action":"publish"}`}},
		FinishReason: "tool_calls",
	})
	ctx := context.Background()
	ag, err := BuildEinoAgent(ctx, mock, "m", "publish report", []agent.BaseTool{report}, 4)
	if err != nil {
		t.Fatal(err)
	}
	rc := &agent.RunContext{
		Ctx:      ctx,
		Vars:     map[string]any{},
		Messages: []messages.Message{messages.Chat(messages.RoleUser, "publish", messages.Meta{})},
		Meta:     messages.Meta{},
	}
	var assistantParts []string
	err = StreamEinoRun(ctx, rc, ag, func(m messages.Message) {
		if m.Type == messages.EventChat && m.Role == messages.RoleAssistant {
			assistantParts = append(assistantParts, m.Content)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.calls != 1 {
		t.Fatalf("report tool calls = %d", report.calls)
		if mock.Calls() != 1 {
			t.Fatalf("model calls = %d, want one terminal tool call", mock.Calls())
		}
	}
	if got := strings.Join(assistantParts, ""); got != answer {
		t.Fatalf("assistant report answer = %q, want byte-identical %q", got, answer)
	}
	if got := middlewares.Final(rc); got != answer {
		t.Fatalf("run final = %q, want byte-identical %q", got, answer)
	}
}
