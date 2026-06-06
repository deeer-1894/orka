package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAIClient talks to any OpenAI-compatible /chat/completions endpoint
// (OpenAI, vLLM, Ollama, Ark, ...).
type OpenAIClient struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// NewOpenAIClient builds a client. baseURL should include the /v1 suffix.
func NewOpenAIClient(baseURL, apiKey string) *OpenAIClient {
	return &OpenAIClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 120 * time.Second},
	}
}

// ---- wire types (OpenAI format) ----

type wireFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function wireFunc `json:"function"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type wireRequest struct {
	Model       string        `json:"model"`
	Messages    []wireMessage `json:"messages"`
	Tools       []wireTool    `json:"tools,omitempty"`
	Temperature float32       `json:"temperature,omitempty"`
}

type wireResponse struct {
	Choices []struct {
		Message      wireMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat implements Client.
func (c *OpenAIClient) Chat(ctx context.Context, req Request) (Response, error) {
	wr := wireRequest{Model: req.Model, Temperature: req.Temperature}
	for _, m := range req.Messages {
		wm := wireMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, Name: m.Name}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
				ID: tc.ID, Type: "function",
				Function: wireFunc{Name: tc.Name, Arguments: tc.Arguments},
			})
		}
		wr.Messages = append(wr.Messages, wm)
	}
	for _, t := range req.Tools {
		var wt wireTool
		wt.Type = "function"
		wt.Function.Name = t.Name
		wt.Function.Description = t.Description
		wt.Function.Parameters = t.Parameters
		wr.Tools = append(wr.Tools, wt)
	}

	body, err := json.Marshal(wr)
	if err != nil {
		return Response{}, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("llm http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return Response{}, fmt.Errorf("llm status %d: %s", resp.StatusCode, string(raw))
	}

	var wresp wireResponse
	if err := json.Unmarshal(raw, &wresp); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	if wresp.Error != nil {
		return Response{}, fmt.Errorf("llm error: %s", wresp.Error.Message)
	}
	if len(wresp.Choices) == 0 {
		return Response{}, fmt.Errorf("llm returned no choices")
	}
	msg := wresp.Choices[0].Message
	out := Response{Content: msg.Content, FinishReason: wresp.Choices[0].FinishReason}
	for _, tc := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return out, nil
}
