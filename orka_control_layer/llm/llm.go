// Package llm is an OpenAI-compatible chat-completions client with tool calling.
// It is a small interface so a mock can be injected in tests (no real endpoint).
package llm

import (
	"context"
	"fmt"
)

// APIError is a typed error from the LLM endpoint, so callers can branch on the
// HTTP status (and inspect the body for provider-specific signals like content
// moderation) instead of matching on error strings.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("llm status %d: %s", e.Status, e.Body) }

// Roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON arguments
}

// ChatMessage is one entry in the model conversation.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // set when Role == tool
	Name       string     `json:"name,omitempty"`         // tool name for tool results
}

// ToolSpec describes a tool exposed to the model.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON schema
}

// Request is a chat-completions request.
type Request struct {
	Model       string
	Messages    []ChatMessage
	Tools       []ToolSpec
	Temperature float32
}

// Usage reports token consumption for a call (when the provider returns it).
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Response is the model's reply.
type Response struct {
	Content      string
	Reasoning    string // reasoning_content from reasoning models (DeepSeek-R1 etc.); "" otherwise
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
}

// Client is the minimal LLM contract.
type Client interface {
	Chat(ctx context.Context, req Request) (Response, error)
}

// StreamingClient is an optional capability: stream the assistant's content
// token-by-token via onDelta, returning the fully-assembled Response (including
// any tool calls) when complete. Callers should type-assert and fall back to
// Chat when a client does not implement it.
type StreamingClient interface {
	ChatStream(ctx context.Context, req Request, onDelta func(string)) (Response, error)
}
