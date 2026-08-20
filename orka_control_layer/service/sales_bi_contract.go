package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

const salesBISkillName = "sales-bi-tool-suite"

const salesBIAssistKey = "sales_bi_assist"
const salesBILockedPrefixKey = "sales_bi_locked_prefix"
const salesBIAnalysisPendingKey = "sales_bi_analysis_pending"
const salesBIReportPendingKey = "sales_bi_report_pending"

var salesBIToolNames = map[string]bool{
	"sales_query_capabilities":       true,
	"sales_query_answer":             true,
	"sales_query_get_result":         true,
	"sales_data_status":              true,
	"product_metadata_resolve":       true,
	"product_metadata_profile":       true,
	"product_metadata_series":        true,
	"product_metadata_batch_profile": true,
	"product_specs_query":            true,
	"product_specs_compare":          true,
	"sales_query_answer_assisted":    true,
	"sales_query_publish":            true,
	"sales_report_generate":          true,
}

var salesBIMetricTerms = []string{
	"销量", "走量", "销售额", "销额", "份额", "贡献度", "排行", "排名", "趋势",
	"同比", "环比", "首销", "平销", "上市影响", "销售表现",
	"sales", "market share", "ranking", "rank", "trend",
}

var salesBIReportTerms = []string{"销售报告", "销售日报", "销售周报", "销售月报", "日报", "周报", "月报"}
var salesBIReportActions = []string{"生成", "制作", "产出", "输出"}

// forceFirstToolClient makes a user-locked Sales BI run enter the MCP workflow
// instead of answering from model memory. Only the first generation is forced;
// later generations can synthesize ordinary non-sealed and multi-step results.
type forceFirstToolClient struct {
	base llm.Client
	once sync.Once
}

func forceFirstSalesBITool(base llm.Client) llm.Client {
	return &forceFirstToolClient{base: base}
}

func (c *forceFirstToolClient) prepare(req llm.Request) llm.Request {
	first := false
	c.once.Do(func() { first = true })
	if !first {
		if salesBIAnalysisPending(req.Messages) {
			return forceSalesBITool(req, "sales_query_publish")
		}
		if salesBIReportPending(req.Messages) {
			return forceSalesBITool(req, "sales_report_generate")
		}
		return req
	}
	tools := make([]llm.ToolSpec, 0, len(req.Tools))
	for _, candidate := range req.Tools {
		if salesBIToolNames[candidate.Name] {
			tools = append(tools, candidate)
		}
	}
	if isSalesBIReportRequest(req.Messages) {
		for _, candidate := range tools {
			if candidate.Name == "sales_report_generate" {
				tools = []llm.ToolSpec{candidate}
				break
			}
		}
	} else if isSalesBIRequest(req.Messages) {
		for _, candidate := range tools {
			if candidate.Name == "sales_query_answer" {
				tools = []llm.ToolSpec{candidate}
				break
			}
		}
	}
	if len(tools) > 0 {
		req.Tools = tools
		req.ToolChoice = "required"
	}
	return req
}

func salesBIReportPending(messages []llm.ChatMessage) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleTool {
			continue
		}
		if messages[i].Name != "sales_report_generate" {
			return false
		}
		var result struct {
			Status   string `json:"status"`
			Action   string `json:"action"`
			ReportID string `json:"report_id"`
		}
		if json.Unmarshal([]byte(messages[i].Content), &result) != nil {
			return false
		}
		return result.Status == "ok" && result.Action == "prepare" && result.ReportID != ""
	}
	return false
}

func isSalesBIReportRequest(messages []llm.ChatMessage) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleUser {
			continue
		}
		query := strings.ToLower(messages[i].Content)
		hasReportTerm := false
		for _, term := range salesBIReportTerms {
			if strings.Contains(query, term) {
				hasReportTerm = true
				break
			}
		}
		if !hasReportTerm {
			return false
		}
		for _, action := range salesBIReportActions {
			if strings.Contains(query, action) {
				return true
			}
		}
		return false
	}
	return false
}

func salesBIAnalysisPending(messages []llm.ChatMessage) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleTool {
			continue
		}
		var result governedResult
		if json.Unmarshal([]byte(messages[i].Content), &result) != nil {
			return false
		}
		analysis := strings.TrimSpace(string(result.Analysis))
		return result.Status == "ok" && result.Metadata.ResponseMode == "grounded_analysis" &&
			analysis != "" && analysis != "null" && analysis != "{}"
	}
	return false
}

func forceSalesBITool(req llm.Request, name string) llm.Request {
	for _, candidate := range req.Tools {
		if candidate.Name == name {
			req.Tools = []llm.ToolSpec{candidate}
			req.ToolChoice = "required"
			return req
		}
	}
	return req
}

func isSalesBIRequest(messages []llm.ChatMessage) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleUser {
			continue
		}
		query := strings.ToLower(messages[i].Content)
		for _, term := range salesBIMetricTerms {
			if strings.Contains(query, term) {
				return true
			}
		}
		return false
	}
	return false
}

// routeSalesBIRequest makes the default "automatic tools" mode deterministic
// for obvious sales questions. An explicitly selected skill always wins.
func routeSalesBIRequest(req ChatRunRequest) ChatRunRequest {
	if strings.TrimSpace(req.ActiveSkill) != "" {
		return req
	}
	messages := []llm.ChatMessage{{Role: llm.RoleUser, Content: req.Message}}
	if isSalesBIReportRequest(messages) || isSalesBIRequest(messages) {
		req.ActiveSkill = salesBISkillName
	}
	return req
}

func (c *forceFirstToolClient) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	prepared := c.prepare(req)
	base := c.base
	if salesBIAnalysisPending(prepared.Messages) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, salesBIAnalysisModelTimeout)
		defer cancel()
		base = salesBISingleAttemptClient(base)
	}
	return base.Chat(ctx, prepared)
}

func (c *forceFirstToolClient) ChatStream(ctx context.Context, req llm.Request, onDelta func(string)) (llm.Response, error) {
	prepared := c.prepare(req)
	base := c.base
	if salesBIAnalysisPending(prepared.Messages) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, salesBIAnalysisModelTimeout)
		defer cancel()
		base = salesBISingleAttemptClient(base)
	}
	if streaming, ok := base.(llm.StreamingClient); ok {
		return streaming.ChatStream(ctx, prepared, onDelta)
	}
	response, err := base.Chat(ctx, prepared)
	if err == nil && onDelta != nil && response.Content != "" {
		onDelta(response.Content)
	}
	return response, err
}

type governedVerdict struct {
	SealedAnswer string
	Terminal     bool
	Continuation string
}

type governedResult struct {
	Status   string          `json:"status"`
	Answer   string          `json:"answer"`
	Analysis json.RawMessage `json:"analysis"`
	Metadata struct {
		ResponseMode string `json:"response_mode"`
	} `json:"metadata"`
}

// governedToolResult separates byte-for-byte answer sealing from whether the
// current run is terminal. Analysis and assist results are sealed but must
// continue through their governed follow-up paths.
func governedToolResult(toolName, raw string) governedVerdict {
	if !salesBIToolNames[toolName] {
		return governedVerdict{}
	}
	var result governedResult
	if json.Unmarshal([]byte(raw), &result) != nil || result.Status == "" || result.Answer == "" {
		return governedVerdict{}
	}
	if result.Status == "ok" {
		if toolName == "sales_report_generate" || result.Metadata.ResponseMode == "sealed" {
			return governedVerdict{SealedAnswer: result.Answer, Terminal: true}
		}
		analysis := strings.TrimSpace(string(result.Analysis))
		if result.Metadata.ResponseMode == "grounded_analysis" && analysis != "" && analysis != "null" && analysis != "{}" {
			return governedVerdict{SealedAnswer: result.Answer, Continuation: "analysis_publish"}
		}
		return governedVerdict{}
	}
	if result.Status == "model_assist_required" {
		return governedVerdict{SealedAnswer: result.Answer, Continuation: "assist"}
	}
	return governedVerdict{SealedAnswer: result.Answer, Terminal: true}
}

type assistResult struct {
	Status string `json:"status"`
	Answer string `json:"answer"`
	Assist struct {
		AssistID   string `json:"assist_id"`
		Question   string `json:"question"`
		ExpiresAt  string `json:"expires_at"`
		Candidates []struct {
			CandidateID        string `json:"candidate_id"`
			ConfirmationToken  string `json:"confirmation_token"`
			ConfirmationPrompt string `json:"confirmation_prompt"`
		} `json:"candidates"`
	} `json:"assist"`
}

// assistPendingFromResult extracts the one selected continuation capability.
// The local deadline caps upstream expiry at five minutes so a day-long chat
// checkpoint can never extend the lifetime of an assist token.
func assistPendingFromResult(raw string, now time.Time) (map[string]any, string, bool) {
	var result assistResult
	if json.Unmarshal([]byte(raw), &result) != nil || result.Status != "model_assist_required" || len(result.Assist.Candidates) == 0 {
		return nil, "", false
	}
	selected := result.Assist.Candidates[0]
	for _, candidate := range result.Assist.Candidates {
		if candidate.ConfirmationPrompt == result.Answer {
			selected = candidate
			break
		}
	}
	if result.Assist.AssistID == "" || selected.CandidateID == "" || selected.ConfirmationToken == "" || selected.ConfirmationPrompt == "" {
		return nil, "", false
	}
	expiresAt := now.Add(5 * time.Minute)
	if upstream, err := time.Parse(time.RFC3339, result.Assist.ExpiresAt); err == nil && upstream.Before(expiresAt) {
		expiresAt = upstream
	}
	pending := map[string]any{
		"assist_id":           result.Assist.AssistID,
		"candidate_id":        selected.CandidateID,
		"confirmation_token":  selected.ConfirmationToken,
		"confirmation_prompt": selected.ConfirmationPrompt,
		"question":            result.Assist.Question,
		"expires_at_ms":       expiresAt.UnixMilli(),
	}
	return pending, selected.ConfirmationPrompt, true
}

func salesBIAuditArgs(toolName string, args map[string]any) map[string]any {
	if !salesBIToolNames[toolName] {
		return args
	}
	redacted, _ := redactSalesBISecrets(args).(map[string]any)
	return redacted
}

func salesBIAuditResult(toolName, raw string) string {
	if !salesBIToolNames[toolName] {
		return raw
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return raw
	}
	redacted, err := json.Marshal(redactSalesBISecrets(value))
	if err != nil {
		return raw
	}
	return string(redacted)
}

func redactSalesBISecrets(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if key == "confirmation_token" || key == "user_turn_token" {
				continue
			}
			out[key] = redactSalesBISecrets(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactSalesBISecrets(item)
		}
		return out
	default:
		return value
	}
}

func publishedAnalysisText(raw string) string {
	var result struct {
		AnalysisText string `json:"analysis_text"`
	}
	_ = json.Unmarshal([]byte(raw), &result)
	return strings.TrimSpace(result.AnalysisText)
}
