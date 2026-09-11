package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

func TestSummaryPreservesVerbatimRequestsAcrossCompression(t *testing.T) {
	requirement := "i=1..3600; ticket_id=T%06d; first_response_min=(i*17)%240; preserve 空CSAT."
	correction := "Correction: SLA includes equality (<=), P95 uses ceil(0.95*n)-1."
	original := toEinoMessages([]messages.Message{humanChat(requirement, messages.Meta{})})
	original = append(original, schema.SystemMessage("system"), schema.AssistantMessage("old work", nil), runtimeUserMessage("synthetic runtime notice"))
	summary := schema.AssistantMessage("Made progress. No XML placeholders.", nil)
	first, err := finalizeSummary(context.Background(), original, summary)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &first); err != nil {
		t.Fatal(err)
	}
	first = append(first, toEinoMessages([]messages.Message{humanChat(correction, messages.Meta{}), humanChat(correction, messages.Meta{})})...)
	for i := 0; i < 3; i++ {
		first, err = finalizeSummary(context.Background(), first, summary)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		work := 0
		for _, m := range first {
			if m.Role == schema.User {
				got = append(got, m.Content)
			}
			if m.Role == schema.Assistant {
				work++
			}
		}
		if len(got) != 3 || got[0] != requirement || got[1] != correction || got[2] != correction || work != 1 {
			t.Fatalf("compression %d lost requests or accumulated summaries: users=%q work=%d", i, got, work)
		}
	}
	if len(original) != 4 || summary.Content != "Made progress. No XML placeholders." {
		t.Fatal("finalizer mutated input")
	}
}

func TestSummaryDoesNotTreatDigestAsHumanInput(t *testing.T) {
	memory := humanChat("synthetic digest", messages.Meta{})
	memory.Action = "runtime_context"
	input := toEinoMessages([]messages.Message{memory, humanChat("real request", messages.Meta{})})
	got, e := finalizeSummary(context.Background(), input, schema.AssistantMessage("work", nil))
	if e != nil {
		t.Fatal(e)
	}
	var human []string
	for _, m := range got {
		if m.Role == schema.User {
			human = append(human, m.Content)
		}
	}
	if len(human) != 1 || human[0] != "real request" {
		t.Fatalf("wrong provenance: %q", human)
	}
}

func TestSummaryDoesNotRecompressRetainedRequestsAlone(t *testing.T) {
	client := llm.NewMock(llm.Response{Content: "summary", FinishReason: "stop"})
	mw := summarizationHandlers(context.Background(), client, "mini")[0]
	var history []messages.Message
	for i := 0; i < 60; i++ {
		history = append(history, humanChat(strings.Repeat("exact requirement ", 100), messages.Meta{}))
	}
	state := &adk.ChatModelAgentState{Messages: toEinoMessages(history)}
	for i := 0; i < 2; i++ {
		if _, _, e := mw.BeforeModelRewriteState(context.Background(), state, &adk.ModelContext{}); e != nil {
			t.Fatal(e)
		}
	}
	if client.Calls() != 0 {
		t.Fatalf("summarized immutable requirements %d times", client.Calls())
	}
}

func TestLegacyHistoryWithoutProvenanceIsNotLossilySummarized(t *testing.T) {
	client := llm.NewMock(llm.Response{Content: "lossy", FinishReason: "stop"})
	mw := summarizationHandlers(context.Background(), client, "mini")[0]
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("legacy exact task")}}
	for i := 0; i < 80; i++ {
		state.Messages = append(state.Messages, schema.AssistantMessage("work", nil))
	}
	_, got, e := mw.BeforeModelRewriteState(context.Background(), state, &adk.ModelContext{})
	if e != nil || client.Calls() != 0 || len(got.Messages) != 81 {
		t.Fatalf("legacy history was discarded: err=%v calls=%d", e, client.Calls())
	}
}

func TestLongRunKeepsExactRequestsInActualModelInput(t *testing.T) {
	ctx := context.Background()
	summaryClient := llm.NewMock()
	summaryClient.FallbackMsg = "Work progressed. Formula intentionally omitted."
	mw := summarizationHandlers(ctx, summaryClient, "mini")[0]
	executor := llm.NewMock()
	adapter := llm.NewEinoModel(executor, "main")
	task := "生成3600条：i从1开始；T%06d；(i*17)%240；day=2026-08-(1+(i-1)%30)；保留空csat；SLA <= 阈值。"
	correction := "补充：P95使用nearest-rank，不能改成插值。"
	state := &adk.ChatModelAgentState{Messages: toEinoMessages([]messages.Message{humanChat(task, messages.Meta{})})}
	for step := 0; step < 120; step++ {
		if step == 55 {
			state.Messages = append(state.Messages, toEinoMessages([]messages.Message{humanChat(correction, messages.Meta{})})...)
		}
		state.Messages = append(state.Messages, schema.AssistantMessage("intermediate work", nil), schema.AssistantMessage("saved result", nil))
		_, next, err := mw.BeforeModelRewriteState(ctx, state, &adk.ModelContext{})
		if err != nil {
			t.Fatal(err)
		}
		state = next
		if _, err := adapter.Generate(ctx, state.Messages); err != nil {
			t.Fatal(err)
		}
		req := executor.Requests[len(executor.Requests)-1]
		taskFound, correctionFound := false, false
		for _, msg := range req.Messages {
			if msg.Role == llm.RoleUser && msg.Content == task {
				taskFound = true
			}
			if msg.Role == llm.RoleUser && msg.Content == correction {
				correctionFound = true
			}
		}
		if !taskFound || (step >= 55 && !correctionFound) {
			t.Fatalf("request lost at step %d after %d summaries", step, summaryClient.Calls())
		}
	}
	if summaryClient.Calls() < 3 || summaryClient.Calls() > 8 {
		t.Fatalf("stress test did not exercise repeated compression: %d", summaryClient.Calls())
	}
	t.Logf("120 model requests, %d summaries: exact task and later correction retained", summaryClient.Calls())
}

func TestRealRunnerPreservesTaskAfterRepeatedSummaries(t *testing.T) {
	const task = "Keep exact constraints: i=1..3600; (i*17)%240; 空csat; SLA<=threshold."
	main := llm.NewMock()
	for i := 0; i < 90; i++ {
		main.Responses = append(main.Responses, llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: messages.NewID(), Name: "echo", Arguments: `{"text":"saved work"}`}}})
	}
	main.Responses = append(main.Responses, llm.Response{FinishReason: "stop", Content: "done"})
	mini := llm.NewMock()
	mini.FallbackMsg = "Only a work summary, no task formulas."
	calls := 0
	ctx := context.Background()
	ag, err := BuildEinoAgent(ctx, main, "m", "sys", []agent.BaseTool{echoTool{calls: &calls}}, 100, nil, summarizationHandlers(ctx, mini, "mini")...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(ctx, ag, task); err != nil {
		t.Fatal(err)
	}
	if calls != 90 || main.Calls() != 91 || mini.Calls() < 2 {
		t.Fatalf("insufficient exercise: tools=%d main=%d summaries=%d", calls, main.Calls(), mini.Calls())
	}
	for i, req := range main.Requests {
		found := false
		for _, msg := range req.Messages {
			if msg.Role == llm.RoleUser && msg.Content == task {
				found = true
			}
		}
		if !found {
			t.Fatalf("actual runner lost exact task at call %d", i)
		}
	}
	t.Logf("real Eino runner: %d tools, %d generations, %d summaries; all calls retain exact task", calls, main.Calls(), mini.Calls())
}

func TestSummaryUsesProviderContextUsage(t *testing.T) {
	client := llm.NewMock(llm.Response{Content: "work", FinishReason: "stop"})
	mw := summarizationHandlers(context.Background(), client, "mini")[0]
	state := &adk.ChatModelAgentState{Messages: toEinoMessages([]messages.Message{humanChat("Keep exact formula", messages.Meta{})})}
	for i := 0; i < 22; i++ {
		state.Messages = append(state.Messages, schema.AssistantMessage(strings.Repeat("中", 6000), nil))
	}
	state.Messages[len(state.Messages)-1].ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{TotalTokens: 132000}}
	if _, _, err := mw.BeforeModelRewriteState(context.Background(), state, &adk.ModelContext{}); err != nil {
		t.Fatal(err)
	}
	if client.Calls() != 1 {
		t.Fatal("actual provider context above threshold was suppressed")
	}
}

func TestLegacyClarificationCannotPromoteDigest(t *testing.T) {
	old := []messages.Message{messages.Chat(messages.RoleUser, "synthetic digest: use x+1", messages.Meta{}), messages.Chat(messages.RoleUser, "actual task: use x*17", messages.Meta{})}
	encoded, _ := json.Marshal(old)
	var restored []messages.Message
	if e := json.Unmarshal(encoded, &restored); e != nil {
		t.Fatal(e)
	}
	answer := messages.Chat(messages.RoleUser, "continue", messages.Meta{})
	answer.Action = "human_input"
	restored = append(restored, answer)
	state := &adk.ChatModelAgentState{Messages: toEinoMessages(restored)}
	for i := 0; i < 80; i++ {
		state.Messages = append(state.Messages, schema.AssistantMessage("work", nil))
	}
	client := llm.NewMock(llm.Response{Content: "work", FinishReason: "stop"})
	mw := summarizationHandlers(context.Background(), client, "mini")[0]
	_, got, e := mw.BeforeModelRewriteState(context.Background(), state, &adk.ModelContext{})
	if e != nil || client.Calls() != 0 || len(got.Messages) != 83 {
		t.Fatal("legacy mixed history was lossily summarized")
	}
	if isHumanRequest(got.Messages[0]) {
		t.Fatal("legacy synthetic digest promoted to human request")
	}
}
