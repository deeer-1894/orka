package service

import (
	"context"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

type requestCaptureClient struct {
	requests []llm.Request
}

func (c *requestCaptureClient) Chat(_ context.Context, req llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, req)
	return llm.Response{Content: "ok"}, nil
}

func TestForceFirstSalesBITool(t *testing.T) {
	base := &requestCaptureClient{}
	client := forceFirstSalesBITool(base)
	req := llm.Request{
		Messages: []llm.ChatMessage{{Role: llm.RoleUser, Content: "最近12个月7000mah以上的手机销量"}},
		Tools: []llm.ToolSpec{
			{Name: "find_skills"},
			{Name: "sales_query_answer"},
			{Name: "product_specs_query"},
		},
	}
	if _, err := client.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	first := base.requests[0]
	if first.ToolChoice != "required" {
		t.Fatalf("first tool choice = %q", first.ToolChoice)
	}
	if len(first.Tools) != 1 || first.Tools[0].Name != "sales_query_answer" {
		t.Fatalf("first tools = %#v", first.Tools)
	}
	second := base.requests[1]
	if second.ToolChoice != "" || len(second.Tools) != 3 {
		t.Fatalf("second request unexpectedly constrained: %#v", second)
	}
}

func TestForceFirstSalesBIReportTool(t *testing.T) {
	base := &requestCaptureClient{}
	client := forceFirstSalesBITool(base)
	req := llm.Request{
		Messages: []llm.ChatMessage{{Role: llm.RoleUser, Content: "请生成2026年6月HTML月报"}},
		Tools: []llm.ToolSpec{
			{Name: "sales_query_answer"},
			{Name: "sales_report_generate"},
			{Name: "product_specs_query"},
		},
	}
	if _, err := client.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	first := base.requests[0]
	if first.ToolChoice != "required" || len(first.Tools) != 1 || first.Tools[0].Name != "sales_report_generate" {
		t.Fatalf("report request tools = %#v choice=%q", first.Tools, first.ToolChoice)
	}
}

func TestForceSalesBIPublishAfterGroundedResult(t *testing.T) {
	base := &requestCaptureClient{}
	client := forceFirstSalesBITool(base)
	tools := []llm.ToolSpec{
		{Name: "sales_query_answer"},
		{Name: "sales_query_publish"},
		{Name: "product_specs_query"},
	}
	first := llm.Request{
		Messages: []llm.ChatMessage{{Role: llm.RoleUser, Content: "最近荣耀销量下降的原因"}},
		Tools:    tools,
	}
	if _, err := client.Chat(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	grounded := `{"status":"ok","answer":"sealed facts","metadata":{"response_mode":"grounded_analysis"},` +
		`"analysis":{"query_id":"sales-20260818-172300-feb810","fact_ledger":[{"fact_id":"f1"}]}}`
	second := llm.Request{
		Messages: []llm.ChatMessage{
			{Role: llm.RoleUser, Content: "最近荣耀销量下降的原因"},
			{Role: llm.RoleTool, Name: "sales_query_answer", Content: grounded},
		},
		Tools: tools,
	}
	if _, err := client.Chat(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	got := base.requests[1]
	if got.ToolChoice != "required" || len(got.Tools) != 1 || got.Tools[0].Name != "sales_query_publish" {
		t.Fatalf("grounded continuation request = %#v", got)
	}
}

func TestForceSalesBIReportPublishAfterPrepare(t *testing.T) {
	base := &requestCaptureClient{}
	client := forceFirstSalesBITool(base)
	tools := []llm.ToolSpec{
		{Name: "sales_query_answer"},
		{Name: "sales_report_generate"},
		{Name: "product_specs_query"},
	}
	first := llm.Request{
		Messages: []llm.ChatMessage{{Role: llm.RoleUser, Content: "请生成2026年6月HTML月报"}},
		Tools:    tools,
	}
	if _, err := client.Chat(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	prepared := `{"status":"ok","action":"prepare","report_id":"monthly-20260630-abcdef","fact_ledger":[{"fact_id":"f1"}]}`
	second := llm.Request{
		Messages: []llm.ChatMessage{
			{Role: llm.RoleUser, Content: "请生成2026年6月HTML月报"},
			{Role: llm.RoleTool, Name: "sales_report_generate", Content: prepared},
		},
		Tools: tools,
	}
	if _, err := client.Chat(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	got := base.requests[1]
	if got.ToolChoice != "required" || len(got.Tools) != 1 || got.Tools[0].Name != "sales_report_generate" {
		t.Fatalf("report continuation request = %#v", got)
	}
}

func TestRouteSalesBIRequestInAutomaticMode(t *testing.T) {
	req := routeSalesBIRequest(ChatRunRequest{Message: "最近12个月6000mah以上的手机销量"})
	if req.ActiveSkill != salesBISkillName {
		t.Fatalf("active skill = %q, want %q", req.ActiveSkill, salesBISkillName)
	}
}

func TestRouteSalesBIRequestRecognizesSalesVolumeWording(t *testing.T) {
	req := routeSalesBIRequest(ChatRunRequest{Message: "2026年6月18日哪款手机走量最大"})
	if req.ActiveSkill != salesBISkillName {
		t.Fatalf("active skill = %q, want %q", req.ActiveSkill, salesBISkillName)
	}
}

func TestRouteSalesBIReportRequestInAutomaticMode(t *testing.T) {
	req := routeSalesBIRequest(ChatRunRequest{Message: "请生成2026年6月HTML月报"})
	if req.ActiveSkill != salesBISkillName {
		t.Fatalf("active skill = %q, want %q", req.ActiveSkill, salesBISkillName)
	}
}

func TestRouteSalesBIRequestPreservesExplicitSkill(t *testing.T) {
	req := routeSalesBIRequest(ChatRunRequest{
		Message:     "最近12个月手机销量",
		ActiveSkill: "researcher",
	})
	if req.ActiveSkill != "researcher" {
		t.Fatalf("active skill = %q, want researcher", req.ActiveSkill)
	}
}

func TestRouteSalesBIRequestLeavesGeneralChatAutomatic(t *testing.T) {
	req := routeSalesBIRequest(ChatRunRequest{Message: "你好"})
	if req.ActiveSkill != "" {
		t.Fatalf("active skill = %q, want empty", req.ActiveSkill)
	}
}

func TestForceFirstSalesBINonMetricKeepsBIRouterChoice(t *testing.T) {
	base := &requestCaptureClient{}
	client := forceFirstSalesBITool(base)
	req := llm.Request{
		Messages: []llm.ChatMessage{{Role: llm.RoleUser, Content: "比较两款手机的电池和充电规格"}},
		Tools: []llm.ToolSpec{
			{Name: "find_skills"},
			{Name: "sales_query_answer"},
			{Name: "product_specs_query"},
			{Name: "product_specs_compare"},
		},
	}
	if _, err := client.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got := base.requests[0]
	if got.ToolChoice != "required" {
		t.Fatalf("tool choice = %q", got.ToolChoice)
	}
	if len(got.Tools) != 3 {
		t.Fatalf("non-metric tools = %#v", got.Tools)
	}
}

func TestGovernedToolResultVerdicts(t *testing.T) {
	tests := []struct {
		name string
		tool string
		raw  string
		want governedVerdict
	}{
		{
			name: "sealed ok is terminal",
			tool: "sales_query_answer",
			raw:  `{"status":"ok","answer":"sealed","metadata":{"response_mode":"sealed"}}`,
			want: governedVerdict{SealedAnswer: "sealed", Terminal: true},
		},
		{
			name: "grounded analysis continues to publish",
			tool: "sales_query_answer",
			raw:  `{"status":"ok","answer":"facts","metadata":{"response_mode":"grounded_analysis"},"analysis":{"query_id":"sales-20260818-120000-abcdef","fact_ledger":[{"fact_id":"f1"}]}}`,
			want: governedVerdict{SealedAnswer: "facts", Continuation: "analysis_publish"},
		},
		{
			name: "model assist continues to user confirmation",
			tool: "sales_query_answer",
			raw:  `{"status":"model_assist_required","answer":"confirm this","assist":{"assist_id":"assist-12345678-abcdef","candidates":[{"candidate_id":"candidate-1","confirmation_token":"secret","confirmation_prompt":"confirm this"}]}}`,
			want: governedVerdict{SealedAnswer: "confirm this", Continuation: "assist"},
		},
		{
			name: "other governed non-ok is terminal",
			tool: "sales_query_answer",
			raw:  `{"status":"clarify_required","answer":"ask again"}`,
			want: governedVerdict{SealedAnswer: "ask again", Terminal: true},
		},
		{
			name: "report publish URL is sealed",
			tool: "sales_report_generate",
			raw:  `{"status":"ok","answer":"报告已生成：http://127.0.0.1/report/1"}`,
			want: governedVerdict{SealedAnswer: "报告已生成：http://127.0.0.1/report/1", Terminal: true},
		},
		{
			name: "report prepare without answer is not governed",
			tool: "sales_report_generate",
			raw:  `{"status":"ok","report_id":"report-1"}`,
			want: governedVerdict{},
		},
		{
			name: "unrelated tool is not governed",
			tool: "unrelated_tool",
			raw:  `{"status":"error","answer":"x"}`,
			want: governedVerdict{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := governedToolResult(tt.tool, tt.raw); got != tt.want {
				t.Fatalf("verdict = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAssistPendingSelectsMatchingPromptAndCapsTTL(t *testing.T) {
	now := time.Date(2026, 8, 18, 17, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	raw := `{"status":"model_assist_required","answer":"请确认候选二",` +
		`"assist":{"assist_id":"assist-12345678-abcdef","question":"哪款手机走量最大",` +
		`"expires_at":"2026-08-18T17:30:00+08:00","candidates":[` +
		`{"candidate_id":"candidate-1","confirmation_token":"wrong-secret","confirmation_prompt":"请确认候选一"},` +
		`{"candidate_id":"candidate-2","confirmation_token":"right-secret","confirmation_prompt":"请确认候选二"}]}}`

	pending, prompt, ok := assistPendingFromResult(raw, now)
	if !ok {
		t.Fatal("assist payload was not parsed")
	}
	if prompt != "请确认候选二" {
		t.Fatalf("prompt = %q", prompt)
	}
	want := map[string]any{
		"assist_id":           "assist-12345678-abcdef",
		"candidate_id":        "candidate-2",
		"confirmation_token":  "right-secret",
		"confirmation_prompt": "请确认候选二",
		"question":            "哪款手机走量最大",
		"expires_at_ms":       now.Add(5 * time.Minute).UnixMilli(),
	}
	for key, value := range want {
		if got := pending[key]; got != value {
			t.Fatalf("pending[%q] = %#v, want %#v", key, got, value)
		}
	}
}
