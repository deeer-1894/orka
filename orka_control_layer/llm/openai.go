package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
//
// Deliberately NO http.Client.Timeout: that is a TOTAL deadline that also cuts
// the streamed response body, so a long (e.g. reasoning-model) generation would
// be killed mid-stream. Instead we bound the connection-level stages via the
// Transport and rely on the request context for cancellation (Kill, or a caller
// deadline like followups). Connection pooling is tuned so the many sequential
// ReAct calls reuse warm TLS connections (also helps provider prefix-caching).
func NewOpenAIClient(baseURL, apiKey string) *OpenAIClient {
	return &OpenAIClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   32,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   15 * time.Second,
				ExpectContinueTimeout: time.Second,
				// Time-to-first-byte cap. Reasoning models think before the first
				// byte on a NON-streamed call, so this is generous; streamed calls
				// get their 200 header immediately and are unaffected.
				ResponseHeaderTimeout: 180 * time.Second,
			},
		},
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
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	Name             string         `json:"name,omitempty"`
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
	Model       string           `json:"model"`
	Messages    []wireReqMessage `json:"messages"`
	Tools       []wireTool       `json:"tools,omitempty"`
	Temperature float32          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
	StreamOpts  *streamOpts      `json:"stream_options,omitempty"`
}

// wireReqMessage is the OUTGOING message; Content is `any` so it can be a plain
// string or a multimodal [{type:text},{type:image_url}] array (vision input).
type wireReqMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type wirePart struct {
	Type     string        `json:"type"` // "text" | "image_url"
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}
type wireImageURL struct {
	URL string `json:"url"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type wireResponse struct {
	Choices []struct {
		Message      wireMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// toWireRequest maps the public Request to the OpenAI wire format.
func toWireRequest(req Request) wireRequest {
	wr := wireRequest{Model: req.Model, Temperature: req.Temperature, MaxTokens: req.MaxTokens}
	for _, m := range req.Messages {
		wm := wireReqMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, Name: m.Name}
		if len(m.Images) > 0 {
			// Multimodal: text part first, then one image_url part per image.
			parts := make([]wirePart, 0, len(m.Images)+1)
			if m.Content != "" {
				parts = append(parts, wirePart{Type: "text", Text: m.Content})
			}
			for _, url := range m.Images {
				parts = append(parts, wirePart{Type: "image_url", ImageURL: &wireImageURL{URL: url}})
			}
			wm.Content = parts
		}
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
	return wr
}

// Chat implements Client.
func (c *OpenAIClient) Chat(ctx context.Context, req Request) (Response, error) {
	wr := toWireRequest(req)

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
		return Response{}, &APIError{Status: resp.StatusCode, Body: string(raw)}
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
	out := Response{Content: msg.Content, Reasoning: msg.ReasoningContent, FinishReason: wresp.Choices[0].FinishReason}
	if u := wresp.Usage; u != nil {
		out.Usage = Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens}
	}
	for _, tc := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	// Reasoning models (e.g. MiMo) sometimes return their whole answer in
	// reasoning_content with an empty content on a non-tool turn. Fall back to the
	// reasoning so downstream consumers always get usable text — notably eino's
	// summarizer, which fatally errors ("summary content is empty") otherwise.
	if out.Content == "" && len(out.ToolCalls) == 0 && out.Reasoning != "" {
		out.Content = out.Reasoning
	}
	return out, nil
}

// ---- streaming ----

type wireStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ChatStream implements StreamingClient: it streams content deltas via onDelta
// and assembles the full Response (content + any tool calls) by the end.
func (c *OpenAIClient) ChatStream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	wr := toWireRequest(req)
	wr.Stream = true
	wr.StreamOpts = &streamOpts{IncludeUsage: true} // ask the provider for a final usage chunk
	body, err := json.Marshal(wr)
	if err != nil {
		return Response{}, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("llm http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return Response{}, &APIError{Status: resp.StatusCode, Body: string(raw)}
	}

	var content, reasoning strings.Builder
	onReasoning := ReasoningSinkFrom(ctx)
	type tcAcc struct {
		id, name string
		args     strings.Builder
	}
	toolAcc := map[int]*tcAcc{}
	var order []int
	finish := ""
	var usage Usage

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk wireStreamChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil {
			return Response{}, fmt.Errorf("llm error: %s", chunk.Error.Message)
		}
		if u := chunk.Usage; u != nil { // final usage chunk (stream_options)
			usage = Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if ch.Delta.ReasoningContent != "" {
			reasoning.WriteString(ch.Delta.ReasoningContent)
			if onReasoning != nil {
				onReasoning(ch.Delta.ReasoningContent)
			}
		}
		if ch.Delta.Content != "" {
			content.WriteString(ch.Delta.Content)
			if onDelta != nil {
				onDelta(ch.Delta.Content)
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			acc := toolAcc[tc.Index]
			if acc == nil {
				acc = &tcAcc{}
				toolAcc[tc.Index] = acc
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			acc.args.WriteString(tc.Function.Arguments)
		}
		if ch.FinishReason != "" {
			finish = ch.FinishReason
		}
	}
	if err := sc.Err(); err != nil {
		return Response{}, fmt.Errorf("stream read: %w", err)
	}

	out := Response{Content: content.String(), Reasoning: reasoning.String(), FinishReason: finish, Usage: usage}
	for _, idx := range order {
		acc := toolAcc[idx]
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: acc.id, Name: acc.name, Arguments: acc.args.String()})
	}
	// Reasoning models (e.g. MiMo) may stream only reasoning_content with no
	// content on a non-tool turn; fall back so downstream never gets empty text.
	if out.Content == "" && len(out.ToolCalls) == 0 && out.Reasoning != "" {
		out.Content = out.Reasoning
	}
	return out, nil
}
