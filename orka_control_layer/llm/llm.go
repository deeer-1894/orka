// Package llm is an OpenAI-compatible chat-completions client with tool calling.
// It is a small interface so a mock can be injected in tests (no real endpoint).
package llm

import "context"

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

// Response is the model's reply.
type Response struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
}

// Client is the minimal LLM contract.
type Client interface {
	Chat(ctx context.Context, req Request) (Response, error)
}
