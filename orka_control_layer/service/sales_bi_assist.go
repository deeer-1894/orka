package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

type salesBIAssistPending struct {
	AssistID           string
	CandidateID        string
	ConfirmationToken  string
	ConfirmationPrompt string
	Question           string
	ExpiresAtMS        int64
}

func salesBIAssistFromVars(vars map[string]any, now time.Time) (salesBIAssistPending, bool, bool) {
	raw, present := vars[salesBIAssistKey]
	if !present {
		return salesBIAssistPending{}, false, false
	}
	pendingMap, ok := raw.(map[string]any)
	if !ok {
		return salesBIAssistPending{}, true, false
	}
	pending := salesBIAssistPending{
		AssistID:           assistString(pendingMap["assist_id"]),
		CandidateID:        assistString(pendingMap["candidate_id"]),
		ConfirmationToken:  assistString(pendingMap["confirmation_token"]),
		ConfirmationPrompt: assistString(pendingMap["confirmation_prompt"]),
		Question:           assistString(pendingMap["question"]),
		ExpiresAtMS:        assistInt64(pendingMap["expires_at_ms"]),
	}
	valid := pending.AssistID != "" && pending.CandidateID != "" && pending.ConfirmationToken != "" &&
		pending.ConfirmationPrompt != "" && pending.ExpiresAtMS > now.UnixMilli()
	return pending, true, valid
}

func assistString(value any) string {
	result, _ := value.(string)
	return result
}

func assistInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		result, _ := typed.Int64()
		return result
	default:
		return 0
	}
}

func (s *ChatService) resumeCheckpointHasSalesBIAssist(ctx context.Context, key string) bool {
	if s.CP == nil || key == "" {
		return false
	}
	checkpoint, err := s.CP.Load(ctx, key)
	if err != nil || checkpoint.Vars == nil {
		return false
	}
	_, ok := checkpoint.Vars[salesBIAssistKey]
	return ok
}

func classifySalesBIAssistReply(ctx context.Context, client llm.Client, modelName string, pending salesBIAssistPending, reply string) (bool, error) {
	input, _ := json.Marshal(map[string]string{
		"original_question":   pending.Question,
		"confirmation_prompt": pending.ConfirmationPrompt,
		"user_reply":          reply,
	})
	response, err := client.Chat(ctx, llm.Request{
		Model:           modelName,
		DisableThinking: true,
		Messages: []llm.ChatMessage{
			{Role: llm.RoleSystem, Content: "Classify whether the user confirms the proposed interpretation or changes its scope. Return JSON only: {\"decision\":\"confirm\"} or {\"decision\":\"changed_scope\"}. Any changed date, metric, product, grouping, or requested meaning is changed_scope."},
			{Role: llm.RoleUser, Content: string(input)},
		},
		Temperature: 0,
		MaxTokens:   256,
	})
	if err != nil {
		return false, err
	}
	var decision struct {
		Decision string `json:"decision"`
	}
	candidate := extractJSON(response.Content)
	if candidate == "" || json.Unmarshal([]byte(candidate), &decision) != nil {
		return false, nil
	}
	return strings.EqualFold(strings.TrimSpace(decision.Decision), "confirm"), nil
}

type boundSalesBIAssistTool struct {
	base agent.BaseTool
	args map[string]any
}

func (t *boundSalesBIAssistTool) Name() string { return "sales_query_answer_assisted" }
func (t *boundSalesBIAssistTool) Description() string {
	return "Continue the user's confirmed Sales BI interpretation. No arguments are required."
}
func (t *boundSalesBIAssistTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}
func (t *boundSalesBIAssistTool) Invoke(ctx context.Context, _ map[string]any) (string, error) {
	return t.base.Invoke(ctx, t.args)
}

func bindSalesBIAssistTool(tools []agent.BaseTool, pending salesBIAssistPending, reply string) ([]agent.BaseTool, bool) {
	bound := append([]agent.BaseTool(nil), tools...)
	for i, candidate := range bound {
		if candidate.Name() != "sales_query_answer_assisted" {
			continue
		}
		args := map[string]any{
			"assist_id":          pending.AssistID,
			"patch":              map[string]any{"selected_candidate_id": pending.CandidateID},
			"confirmation_token": pending.ConfirmationToken,
			"user_reply":         reply,
			"user_turn_token":    "turn-" + messages.NewID(),
		}
		bound[i] = &boundSalesBIAssistTool{base: candidate, args: args}
		return bound, true
	}
	return tools, false
}
func withoutSalesBIAssistedTool(tools []agent.BaseTool) []agent.BaseTool {
	filtered := make([]agent.BaseTool, 0, len(tools))
	for _, candidate := range tools {
		if candidate.Name() != "sales_query_answer_assisted" {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func salesBIBaseClient(client llm.Client) llm.Client {
	if forced, ok := client.(*forceFirstToolClient); ok {
		return forced.base
	}
	return client
}

type forceFirstNamedToolClient struct {
	base llm.Client
	name string
	once sync.Once
}

func forceFirstNamedTool(base llm.Client, name string) llm.Client {
	return &forceFirstNamedToolClient{base: base, name: name}
}

func (c *forceFirstNamedToolClient) prepare(req llm.Request) llm.Request {
	first := false
	c.once.Do(func() { first = true })
	if !first {
		return req
	}
	for _, candidate := range req.Tools {
		if candidate.Name == c.name {
			req.Tools = []llm.ToolSpec{candidate}
			req.ToolChoice = "required"
			break
		}
	}
	return req
}

func (c *forceFirstNamedToolClient) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	return c.base.Chat(ctx, c.prepare(req))
}

func (c *forceFirstNamedToolClient) ChatStream(ctx context.Context, req llm.Request, onDelta func(string)) (llm.Response, error) {
	prepared := c.prepare(req)
	if streaming, ok := c.base.(llm.StreamingClient); ok {
		return streaming.ChatStream(ctx, prepared, onDelta)
	}
	response, err := c.base.Chat(ctx, prepared)
	if err == nil && onDelta != nil && response.Content != "" {
		onDelta(response.Content)
	}
	return response, err
}

func prepareSalesBIAssistResume(ctx context.Context, rc *agent.RunContext, tools []agent.BaseTool, model llm.Client, modelName, reply string) ([]agent.BaseTool, llm.Client, bool, error) {
	pending, present, valid := salesBIAssistFromVars(rc.Vars, time.Now())
	if !present {
		return tools, model, false, nil
	}
	delete(rc.Vars, salesBIAssistKey)
	base := salesBIBaseClient(model)
	if !valid {
		return withoutSalesBIAssistedTool(tools), forceFirstSalesBITool(base), true, nil
	}
	confirmed := strings.TrimSpace(reply) == "确认"
	if !confirmed {
		var err error
		confirmed, err = classifySalesBIAssistReply(ctx, base, modelName, pending, reply)
		if err != nil {
			return nil, nil, true, err
		}
	}
	if !confirmed {
		return withoutSalesBIAssistedTool(tools), forceFirstNamedTool(base, "sales_query_answer"), true, nil
	}
	bound, ok := bindSalesBIAssistTool(tools, pending, reply)
	if !ok {
		return nil, nil, true, fmt.Errorf("Sales BI assist resume requires sales_query_answer_assisted")
	}
	return bound, forceFirstNamedTool(base, "sales_query_answer_assisted"), true, nil
}
