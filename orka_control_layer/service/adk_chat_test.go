package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/checkpoint"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/message_utils"
	"github.com/orka-oss/orka_control_layer/obs"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
)

type collector struct {
	mu   sync.Mutex
	msgs []messages.Message
}

func (c *collector) sink(m messages.Message) {
	c.mu.Lock()
	c.msgs = append(c.msgs, m)
	c.mu.Unlock()
}

func (c *collector) byType(t messages.EventType) []messages.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []messages.Message
	for _, m := range c.msgs {
		if m.Type == t {
			out = append(out, m)
		}
	}
	return out
}

func (c *collector) clarifyKey() string {
	for _, m := range c.byType(messages.EventClarify) {
		if cm, ok := m.Payload.(messages.ClarifyMessage); ok {
			return cm.ResumeKey
		}
	}
	return ""
}

func testService(t *testing.T, mainLLM llm.Client) (*ChatService, checkpoint.Store) {
	t.Helper()
	cfg := &config.Config{}
	cfg.LLM.Model = "m"
	cfg.Agent.CheckpointTTLSec = 3600
	cpStore := checkpoint.NewMemoryStore()
	msg := message_utils.New(nil, 1.0, nil) // no Mongo
	svc := NewChatService(cfg, mainLLM, mainLLM, cpStore, msg, obs.NewMetrics(), nil)
	svc.DisableSummary = true // deterministic: don't let the summarizer consume scripted mock responses
	return svc, cpStore
}

func TestChat_NormalRun(t *testing.T) {
	svc, _ := testService(t, llm.NewMock(llm.Response{Content: "hello there", FinishReason: "stop"}))
	col := &collector{}
	svc.Run(context.Background(), ChatRunRequest{Message: "hi", ConversationID: "c1"}, col.sink)

	if chats := col.byType(messages.EventChat); len(chats) == 0 || chats[len(chats)-1].Content != "hello there" {
		t.Fatalf("assistant chat missing: %+v", chats)
	}
	done := col.byType(messages.EventTask)
	if len(done) == 0 || done[len(done)-1].Action != "done" {
		t.Fatalf("task done missing: %+v", done)
	}
}

func TestChat_ClarifyResumeAndDuplicateRejected(t *testing.T) {
	mock := llm.NewMock(
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "1", Name: "clarify", Arguments: `{"question":"which?","options":["A","B"]}`}}, FinishReason: "tool_calls"},
		llm.Response{Content: "resolved with A", FinishReason: "stop"},
	)
	svc, _ := testService(t, mock)

	// 1) run -> clarify + checkpoint saved
	col := &collector{}
	svc.Run(context.Background(), ChatRunRequest{Message: "ambiguous", ConversationID: "c1"}, col.sink)

	key := col.clarifyKey()
	if key == "" {
		t.Fatalf("no clarify resume_key emitted: %+v", col.msgs)
	}
	if paused := col.byType(messages.EventTask); len(paused) == 0 || paused[len(paused)-1].Action != "paused" {
		t.Fatalf("expected task paused, got %+v", paused)
	}

	// 2) resume -> final answer + done
	col2 := &collector{}
	svc.Run(context.Background(), ChatRunRequest{Message: "A", ConversationID: "c1", ResumeKey: key}, col2.sink)
	if chats := col2.byType(messages.EventChat); len(chats) == 0 || chats[len(chats)-1].Content != "resolved with A" {
		t.Fatalf("resume final missing: %+v", col2.msgs)
	}
	if done := col2.byType(messages.EventTask); len(done) == 0 || done[len(done)-1].Action != "done" {
		t.Fatalf("resume task done missing: %+v", done)
	}

	// 3) duplicate resume with same key -> rejected (checkpoint already claimed)
	col3 := &collector{}
	svc.Run(context.Background(), ChatRunRequest{Message: "A", ConversationID: "c1", ResumeKey: key}, col3.sink)
	failed := col3.byType(messages.EventTask)
	if len(failed) == 0 || failed[len(failed)-1].Action != "failed" {
		t.Fatalf("duplicate resume should fail, got %+v", col3.msgs)
	}
}

func TestChat_AssistCreatesSecretSafeCheckpoint(t *testing.T) {
	const token = "confirm-secret-must-not-persist"
	const prompt = "我将这个问题理解为：2026年6月走量最大的机型。请确认。"
	query := &staticResultTool{
		name: "sales_query_answer",
		result: `{"status":"model_assist_required","answer":"` + prompt + `","assist":{` +
			`"assist_id":"assist-12345678-abcdef","question":"2026年6月18日哪款手机走量最大",` +
			`"expires_at":"2099-01-01T00:00:00+08:00","candidates":[` +
			`{"candidate_id":"candidate-1","confirmation_token":"` + token + `","confirmation_prompt":"` + prompt + `"}]}}`,
	}
	mock := llm.NewMock(llm.Response{
		ToolCalls:    []llm.ToolCall{{ID: "q1", Name: query.name, Arguments: `{}`}},
		FinishReason: "tool_calls",
	})
	svc, store := testService(t, mock)
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{query}, nil, nil
	}
	col := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "2026年6月18日哪款手机走量最大", ConversationID: "assist-c1",
	}, col.sink)

	key := col.clarifyKey()
	if key == "" {
		t.Fatalf("no assist checkpoint emitted: %+v", col.msgs)
	}
	clarifies := col.byType(messages.EventClarify)
	clarify, ok := clarifies[len(clarifies)-1].Payload.(messages.ClarifyMessage)
	if !ok || clarify.Question != prompt {
		t.Fatalf("clarify = %#v, want exact prompt %q", clarifies[len(clarifies)-1].Payload, prompt)
	}
	saved, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	pending, ok := saved.Vars[salesBIAssistKey].(map[string]any)
	if !ok || pending["confirmation_token"] != token {
		t.Fatalf("checkpoint pending = %#v", saved.Vars[salesBIAssistKey])
	}
	encoded, err := json.Marshal(col.msgs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("confirmation token leaked into emitted/persisted message payload")
	}
}

type captureArgsTool struct {
	name   string
	result string
	calls  int
	args   map[string]any
}

func (t *captureArgsTool) Name() string        { return t.name }
func (t *captureArgsTool) Description() string { return t.name }
func (t *captureArgsTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *captureArgsTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	t.calls++
	t.args = args
	return t.result, nil
}

func TestChat_AssistConfirmationInvokesBoundToolWithoutModelSecret(t *testing.T) {
	const token = "confirm-resume-secret"
	const prompt = "我将这个问题理解为：2026年6月走量最大的机型。请确认。"
	const final = "密封续调结果"
	query := &staticResultTool{
		name: "sales_query_answer",
		result: `{"status":"model_assist_required","answer":"` + prompt + `","assist":{` +
			`"assist_id":"assist-12345678-abcdef","question":"2026年6月18日哪款手机走量最大",` +
			`"expires_at":"2099-01-01T00:00:00+08:00","candidates":[` +
			`{"candidate_id":"candidate-1","confirmation_token":"` + token + `","confirmation_prompt":"` + prompt + `"}]}}`,
	}
	assisted := &captureArgsTool{
		name:   "sales_query_answer_assisted",
		result: `{"status":"ok","answer":"` + final + `","metadata":{"response_mode":"sealed"}}`,
	}
	mock := llm.NewMock(
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "q1", Name: query.name, Arguments: `{}`}},
			FinishReason: "tool_calls",
		},
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "a1", Name: assisted.name, Arguments: `{}`}},
			FinishReason: "tool_calls",
		},
	)
	svc, _ := testService(t, mock)
	resumeUsedSalesSkill := false
	svc.ToolsFor = func(_ context.Context, req ChatRunRequest) ([]agent.BaseTool, func(), error) {
		if req.ResumeKey != "" {
			resumeUsedSalesSkill = req.ActiveSkill == salesBISkillName
		}
		return []agent.BaseTool{query, assisted}, nil, nil
	}
	first := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "2026年6月18日哪款手机走量最大", ConversationID: "assist-confirm-c1", ActiveSkill: salesBISkillName,
	}, first.sink)
	key := first.clarifyKey()
	if key == "" {
		t.Fatalf("no assist checkpoint: %+v", first.msgs)
	}

	resumed := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "确认", ConversationID: "assist-confirm-c1", ResumeKey: key,
	}, resumed.sink)
	if !resumeUsedSalesSkill {
		t.Fatal("resume did not restore Sales BI routing before tool discovery")
	}
	if assisted.calls != 1 {
		t.Fatalf("assisted tool calls = %d, want 1; model calls=%d events=%+v", assisted.calls, mock.Calls(), resumed.msgs)
	}
	if len(mock.Requests) < 2 || len(mock.Requests[1].Tools) != 1 || mock.Requests[1].Tools[0].Name != assisted.name {
		t.Fatalf("explicit confirmation did not proceed directly to assisted tool: %#v", mock.Requests)
	}
	if assisted.args["assist_id"] != "assist-12345678-abcdef" || assisted.args["confirmation_token"] != token {
		t.Fatalf("assisted credentials = %#v", assisted.args)
	}
	if assisted.args["user_reply"] != "确认" {
		t.Fatalf("user_reply = %#v", assisted.args["user_reply"])
	}
	patch, ok := assisted.args["patch"].(map[string]any)
	if !ok || patch["selected_candidate_id"] != "candidate-1" {
		t.Fatalf("assisted patch = %#v", assisted.args["patch"])
	}
	turnToken, _ := assisted.args["user_turn_token"].(string)
	if !strings.HasPrefix(turnToken, "turn-") {
		t.Fatalf("user_turn_token = %q", turnToken)
	}
	chats := resumed.byType(messages.EventChat)
	if len(chats) == 0 || chats[len(chats)-1].Content != final {
		t.Fatalf("sealed assisted result missing: %+v", chats)
	}
	requests, err := json.Marshal(mock.Requests)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(requests), token) || strings.Contains(string(requests), turnToken) {
		t.Fatal("code-injected assist capability leaked into a model request")
	}
	events, err := json.Marshal(resumed.msgs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(events), token) || strings.Contains(string(events), turnToken) {
		t.Fatal("code-injected assist capability leaked into emitted/persisted messages")
	}
}

type failAfterFirstLLM struct {
	first llm.Response
	calls int
}

func (c *failAfterFirstLLM) Chat(_ context.Context, _ llm.Request) (llm.Response, error) {
	c.calls++
	if c.calls == 1 {
		return c.first, nil
	}
	return llm.Response{}, errors.New("assist classifier failed")
}

func TestChat_AssistClassifierFailureTerminatesResume(t *testing.T) {
	const prompt = "请确认查询口径。"
	query := &staticResultTool{
		name: "sales_query_answer",
		result: `{"status":"model_assist_required","answer":"` + prompt + `","assist":{` +
			`"assist_id":"assist-failure-abcdef","question":"哪款手机走量最大",` +
			`"expires_at":"2099-01-01T00:00:00+08:00","candidates":[` +
			`{"candidate_id":"candidate-1","confirmation_token":"failure-secret","confirmation_prompt":"` + prompt + `"}]}}`,
	}
	client := &failAfterFirstLLM{first: llm.Response{
		ToolCalls:    []llm.ToolCall{{ID: "q1", Name: query.name, Arguments: `{}`}},
		FinishReason: "tool_calls",
	}}
	svc, _ := testService(t, client)
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{query}, nil, nil
	}
	first := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "哪款手机走量最大", ConversationID: "assist-failure-c1", ActiveSkill: salesBISkillName,
	}, first.sink)
	key := first.clarifyKey()
	if key == "" {
		t.Fatalf("no assist checkpoint: %+v", first.msgs)
	}

	resumed := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "按这个理解", ConversationID: "assist-failure-c1", ResumeKey: key,
	}, resumed.sink)
	tasks := resumed.byType(messages.EventTask)
	if len(tasks) == 0 || tasks[len(tasks)-1].Action != "failed" {
		t.Fatalf("classifier failure left resume non-terminal: %+v", resumed.msgs)
	}
}

func TestChat_ExpiredAssistNeverExposesOrCallsAssistedTool(t *testing.T) {
	const prompt = "请确认已过期的候选。"
	query := &staticResultTool{
		name: "sales_query_answer",
		result: `{"status":"model_assist_required","answer":"` + prompt + `","assist":{` +
			`"assist_id":"assist-expired-abcdef","question":"哪款手机走量最大",` +
			`"expires_at":"2000-01-01T00:00:00+08:00","candidates":[` +
			`{"candidate_id":"candidate-1","confirmation_token":"expired-secret","confirmation_prompt":"` + prompt + `"}]}}`,
	}
	assisted := &captureArgsTool{
		name:   "sales_query_answer_assisted",
		result: `{"status":"ok","answer":"must not run","metadata":{"response_mode":"sealed"}}`,
	}
	mock := llm.NewMock(
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "q1", Name: query.name, Arguments: `{}`}},
			FinishReason: "tool_calls",
		},
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "q2", Name: query.name, Arguments: `{"question":"确认"}`}},
			FinishReason: "tool_calls",
		},
	)
	svc, _ := testService(t, mock)
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{query, assisted}, nil, nil
	}
	first := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "哪款手机走量最大", ConversationID: "assist-expired-c1", ActiveSkill: salesBISkillName,
	}, first.sink)
	key := first.clarifyKey()
	if key == "" {
		t.Fatalf("no expired assist checkpoint: %+v", first.msgs)
	}

	resumed := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "确认", ConversationID: "assist-expired-c1", ResumeKey: key,
	}, resumed.sink)
	if assisted.calls != 0 {
		t.Fatalf("expired pending invoked assisted tool %d times", assisted.calls)
	}
	if mock.Calls() != 2 {
		t.Fatalf("model calls = %d, want initial query + fresh query without classifier", mock.Calls())
	}
	if len(mock.Requests) < 2 {
		t.Fatalf("captured model requests = %d", len(mock.Requests))
	}
	for _, tool := range mock.Requests[1].Tools {
		if tool.Name == "sales_query_answer_assisted" {
			t.Fatal("expired resume exposed sales_query_answer_assisted to the model")
		}
	}
	if query.calls != 2 {
		t.Fatalf("fresh query calls = %d, want 2 total", query.calls)
	}
}

func TestChat_ChangedAssistScopeStartsFreshSalesQuery(t *testing.T) {
	const token = "changed-scope-secret"
	const prompt = "请确认原来的机型范围。"
	query := &staticResultTool{
		name: "sales_query_answer",
		result: `{"status":"model_assist_required","answer":"` + prompt + `","assist":{` +
			`"assist_id":"assist-changed-abcdef","question":"哪款手机走量最大",` +
			`"expires_at":"2099-01-01T00:00:00+08:00","candidates":[` +
			`{"candidate_id":"candidate-1","confirmation_token":"` + token + `","confirmation_prompt":"` + prompt + `"}]}}`,
	}
	assisted := &captureArgsTool{
		name:   "sales_query_answer_assisted",
		result: `{"status":"ok","answer":"must not run","metadata":{"response_mode":"sealed"}}`,
	}
	mock := llm.NewMock(
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "q1", Name: query.name, Arguments: `{}`}},
			FinishReason: "tool_calls",
		},
		llm.Response{Content: `{"decision":"changed_scope"}`, FinishReason: "stop"},
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "q2", Name: query.name, Arguments: `{"question":"改查7月"}`}},
			FinishReason: "tool_calls",
		},
	)
	svc, _ := testService(t, mock)
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{query, assisted}, nil, nil
	}
	first := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "哪款手机走量最大", ConversationID: "assist-changed-c1", ActiveSkill: salesBISkillName,
	}, first.sink)
	key := first.clarifyKey()
	if key == "" {
		t.Fatalf("no assist checkpoint: %+v", first.msgs)
	}

	resumed := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "改查7月的销量", ConversationID: "assist-changed-c1", ResumeKey: key,
	}, resumed.sink)
	if assisted.calls != 0 {
		t.Fatalf("changed scope invoked assisted tool %d times", assisted.calls)
	}
	if query.calls != 2 || mock.Calls() != 3 {
		t.Fatalf("query calls=%d model calls=%d, want 2 and 3", query.calls, mock.Calls())
	}
	if mock.Requests[1].Model != "m" || mock.Requests[1].MaxTokens != 256 {
		t.Fatalf("assist classifier request = model %q max_tokens %d, want m and 256", mock.Requests[1].Model, mock.Requests[1].MaxTokens)
	}
	if len(mock.Requests[2].Tools) != 1 || mock.Requests[2].Tools[0].Name != "sales_query_answer" {
		t.Fatalf("fresh-scope tools = %#v", mock.Requests[2].Tools)
	}
	requests, err := json.Marshal(mock.Requests)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(requests), token) {
		t.Fatal("confirmation token leaked into changed-scope model requests")
	}
}

func TestChat_AnalysisRetriesPublishAfterEmptyContinuation(t *testing.T) {
	const prefix = "密封数据表"
	const analysisText = "【核心洞察】\n- 销量下降。"
	query := &staticResultTool{
		name: "sales_query_answer",
		result: `{"status":"ok","answer":"` + prefix + `","metadata":{"response_mode":"grounded_analysis"},` +
			`"analysis":{"query_id":"sales-20260818-retry-abcdef","fact_ledger":[{"fact_id":"f1","value":1}]}}`,
	}
	publish := &captureArgsTool{
		name:   "sales_query_publish",
		result: `{"analysis_text":"【核心洞察】\n- 销量下降。"}`,
	}
	mock := llm.NewMock(
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "q1", Name: query.name, Arguments: `{}`}},
			FinishReason: "tool_calls",
		},
		llm.Response{FinishReason: "stop"},
		llm.Response{
			ToolCalls: []llm.ToolCall{{
				ID: "p1", Name: publish.name,
				Arguments: `{"query_id":"sales-20260818-retry-abcdef","narrative":{"summary":"销量下降"}}`,
			}},
			FinishReason: "tool_calls",
		},
	)
	svc, _ := testService(t, mock)
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{query, publish}, nil, nil
	}
	col := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "最近荣耀销量下降的原因", ConversationID: "analysis-retry-c1", ActiveSkill: salesBISkillName,
	}, col.sink)
	if query.calls != 1 || publish.calls != 1 {
		t.Fatalf("tool calls: query=%d publish=%d", query.calls, publish.calls)
	}
	if mock.Calls() != 3 {
		t.Fatalf("model calls = %d, want query + empty continuation + publish retry", mock.Calls())
	}
	var parts []string
	for _, chat := range col.byType(messages.EventChat) {
		if chat.Role == messages.RoleAssistant {
			parts = append(parts, chat.Content)
		}
	}
	want := prefix + "\n\n" + analysisText
	if got := strings.Join(parts, "\n\n"); got != want {
		t.Fatalf("assistant answer = %q, want %q", got, want)
	}
}

type reportSequenceTool struct {
	calls []map[string]any
}

func (t *reportSequenceTool) Name() string        { return "sales_report_generate" }
func (t *reportSequenceTool) Description() string { return "generate report" }
func (t *reportSequenceTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *reportSequenceTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	t.calls = append(t.calls, args)
	if len(t.calls) == 1 {
		return `{"status":"ok","action":"prepare","report_id":"monthly-20260630-abcdef","fact_ledger":[{"fact_id":"f1","value":1}]}`, nil
	}
	return `{"status":"ok","action":"publish","answer":"报告已生成：http://localhost:8090/reports/monthly-20260630-abcdef.html"}`, nil
}

func TestChat_ReportFallsBackToPublishAfterEmptyContinuation(t *testing.T) {
	report := &reportSequenceTool{}
	mock := llm.NewMock(
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "r1", Name: report.Name(), Arguments: `{"action":"prepare","report_type":"monthly","period":"2026年6月"}`}},
			FinishReason: "tool_calls",
		},
		llm.Response{FinishReason: "stop"},
	)
	svc, _ := testService(t, mock)
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{report}, nil, nil
	}
	col := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "请生成2026年6月HTML月报", ConversationID: "report-fallback-c1", ActiveSkill: salesBISkillName,
	}, col.sink)
	if len(report.calls) != 2 {
		t.Fatalf("report calls = %d, want prepare and fallback publish", len(report.calls))
	}
	if report.calls[1]["action"] != "publish" || report.calls[1]["report_id"] != "monthly-20260630-abcdef" {
		t.Fatalf("fallback publish args = %#v", report.calls[1])
	}
	narrative, ok := report.calls[1]["narrative"].(map[string]any)
	if !ok || len(narrative) != 0 {
		t.Fatalf("fallback narrative = %#v, want empty object", report.calls[1]["narrative"])
	}
	chats := col.byType(messages.EventChat)
	const want = "报告已生成：http://localhost:8090/reports/monthly-20260630-abcdef.html"
	if len(chats) == 0 || chats[len(chats)-1].Content != want {
		t.Fatalf("sealed report answer missing: %+v", chats)
	}
	tasks := col.byType(messages.EventTask)
	if len(tasks) == 0 || tasks[len(tasks)-1].Action != "done" {
		t.Fatalf("report fallback task not done: %+v", tasks)
	}
}

func TestChat_AnalysisUsesLedgerFallbackWhenModelCannotPublish(t *testing.T) {
	query := &staticResultTool{
		name: "sales_query_answer",
		result: `{"status":"ok","answer":"密封数据表","metadata":{"response_mode":"grounded_analysis"},` +
			`"analysis":{"query_id":"sales-20260818-180000-abcdef","fact_ledger":[{"fact_id":"f1","value":1}]}}`,
	}
	publish := &captureArgsTool{
		name:   "sales_query_publish",
		result: `{"analysis_text":"【核心洞察】\n- 事实账本兜底。"}`,
	}
	mock := llm.NewMock(
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "q1", Name: query.name, Arguments: `{}`}},
			FinishReason: "tool_calls",
		},
		llm.Response{FinishReason: "stop"},
		llm.Response{FinishReason: "stop"},
	)
	svc, _ := testService(t, mock)
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{query, publish}, nil, nil
	}
	col := &collector{}
	svc.Run(context.Background(), ChatRunRequest{
		Message: "最近销量下降的原因", ConversationID: "analysis-fallback-c1", ActiveSkill: salesBISkillName,
	}, col.sink)
	if publish.calls != 1 {
		t.Fatalf("ledger fallback publish calls = %d, want 1", publish.calls)
	}
	if mock.Calls() != 3 {
		t.Fatalf("model calls = %d, want query + normal publish + one dedicated attempt", mock.Calls())
	}
	if publish.args["query_id"] != "sales-20260818-180000-abcdef" {
		t.Fatalf("fallback query_id = %#v", publish.args["query_id"])
	}
	narrative, ok := publish.args["narrative"].(map[string]any)
	if !ok || len(narrative) != 0 {
		t.Fatalf("fallback narrative = %#v, want empty map for engine ledger fallback", publish.args["narrative"])
	}
	chats := col.byType(messages.EventChat)
	if len(chats) < 2 {
		t.Fatalf("assistant parts = %+v", chats)
	}
	if got := chats[len(chats)-1].Content; got != "【核心洞察】\n- 事实账本兜底。" {
		t.Fatalf("fallback analysis = %q", got)
	}
}

// blockingLLM blocks until the context is cancelled.
type blockingLLM struct{}

func (blockingLLM) Chat(ctx context.Context, _ llm.Request) (llm.Response, error) {
	<-ctx.Done()
	return llm.Response{}, ctx.Err()
}

func TestChat_KillCancels(t *testing.T) {
	svc, _ := testService(t, blockingLLM{})
	col := &collector{}
	done := make(chan struct{})
	go func() {
		svc.Run(context.Background(), ChatRunRequest{Message: "long task", ConversationID: "killme"}, col.sink)
		close(done)
	}()

	// wait until the run registers, then kill
	deadline := time.After(2 * time.Second)
	for {
		if svc.Kill("killme") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("run never registered for kill")
		case <-time.After(10 * time.Millisecond):
		}
	}

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("run did not stop within 1s of kill")
	}
	failed := col.byType(messages.EventTask)
	if len(failed) == 0 || failed[len(failed)-1].Action != "failed" {
		t.Fatalf("expected task failed after kill, got %+v", col.msgs)
	}
}
