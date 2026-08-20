package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

const salesBIAnalysisModelTimeout = 90 * time.Second

func publishPendingSalesBIAnalysis(ctx context.Context, rc *agent.RunContext, client llm.Client, model, instruction string, tools []agent.BaseTool) error {
	raw := rc.Str(salesBIAnalysisPendingKey)
	if raw == "" {
		return nil
	}
	var publish agent.BaseTool
	for _, candidate := range tools {
		if candidate.Name() == "sales_query_publish" {
			publish = candidate
			break
		}
	}
	if publish == nil {
		return errors.New("Sales BI analysis continuation requires sales_query_publish")
	}
	request := llm.Request{
		Model:           model,
		DisableThinking: true,
		Messages: []llm.ChatMessage{
			{Role: llm.RoleSystem, Content: instruction},
			{Role: llm.RoleUser, Content: "The governed sales_query_answer result follows. Read analysis.fact_ledger, write only grounded narrative, and call sales_query_publish exactly once. Do not repeat or rewrite the sealed answer.\n\n" + raw},
		},
		Tools: []llm.ToolSpec{{
			Name:        publish.Name(),
			Description: publish.Description(),
			Parameters:  publish.Schema(),
		}},
		ToolChoice: "required",
	}
	base := salesBISingleAttemptClient(client)
	modelCtx, cancel := context.WithTimeout(ctx, salesBIAnalysisModelTimeout)
	response, modelErr := base.Chat(modelCtx, request)
	cancel()
	var args map[string]any
	if modelErr == nil {
		middlewares.AddRunTokens(rc, response.Usage.TotalTokens)
		var call *llm.ToolCall
		for i := range response.ToolCalls {
			if response.ToolCalls[i].Name == publish.Name() {
				call = &response.ToolCalls[i]
				break
			}
		}
		if call != nil {
			candidate := parseJSONArgs(call.Arguments)
			if _, invalid := candidate["_raw"]; !invalid {
				args = candidate
			}
		}
	}
	if args == nil {
		queryID := pendingAnalysisQueryID(raw)
		if queryID == "" {
			if modelErr != nil {
				return modelErr
			}
			return errors.New("model completed without sales_query_publish tool call")
		}
		args = map[string]any{"query_id": queryID, "narrative": map[string]any{}}
	}
	result, err := publish.Invoke(ctx, args)
	if err != nil {
		return err
	}
	middlewares.AddRunTools(rc, 1)
	rc.Emit(messages.Tool("call", map[string]any{
		"tool":   publish.Name(),
		"args":   salesBIAuditArgs(publish.Name(), args),
		"result": salesBIAuditResult(publish.Name(), result),
	}, rc.Meta))
	delete(rc.Vars, salesBIAnalysisPendingKey)
	analysisText := publishedAnalysisText(result)
	if analysisText == "" {
		verdict := governedToolResult(publish.Name(), result)
		if verdict.Terminal {
			answer := messages.Chat(messages.RoleAssistant, verdict.SealedAnswer, rc.Meta)
			rc.Messages = append(rc.Messages, answer)
			rc.Emit(answer)
			middlewares.SetFinal(rc, verdict.SealedAnswer)
			return nil
		}
		return errors.New("sales_query_publish returned no analysis_text")
	}
	part := messages.Chat(messages.RoleAssistant, analysisText, rc.Meta)
	rc.Messages = append(rc.Messages, part)
	rc.Emit(part)
	middlewares.SetFinal(rc, rc.Str(salesBILockedPrefixKey)+"\n\n"+analysisText)
	return nil
}

func pendingAnalysisQueryID(raw string) string {
	var result struct {
		Analysis struct {
			QueryID string `json:"query_id"`
		} `json:"analysis"`
	}
	if json.Unmarshal([]byte(raw), &result) != nil {
		return ""
	}
	return result.Analysis.QueryID
}

func salesBISingleAttemptClient(client llm.Client) llm.Client {
	base := salesBIBaseClient(client)
	if retry, ok := base.(*llm.Retry); ok {
		return retry.Client
	}
	return base
}
