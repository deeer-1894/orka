package llm

import (
	"context"
	"strings"
	"time"
)

// DemoClient is a deterministic offline client for HTTP smoke tests (no real
// endpoint). It echoes input, and triggers a clarify when the user message
// contains "clarify" (until a clarify has already been asked).
type DemoClient struct{}

// NewDemo returns a DemoClient.
func NewDemo() *DemoClient { return &DemoClient{} }

// Chat implements Client.
func (DemoClient) Chat(_ context.Context, req Request) (Response, error) {
	lastUser := ""
	hasClarify := false
	hasToolResult := false
	for _, m := range req.Messages {
		if m.Role == RoleUser {
			lastUser = m.Content
		}
		if m.Role == RoleTool {
			hasToolResult = true
		}
		if m.Role == RoleAssistant {
			for _, tc := range m.ToolCalls {
				if tc.Name == "clarify" {
					hasClarify = true
				}
			}
		}
	}
	// After a tool result has been fed back, produce a final answer.
	if hasToolResult {
		return Response{Content: "Done — the tool completed successfully.", FinishReason: "stop"}, nil
	}
	// Drive a file_write tool call to exercise the tool path.
	if strings.Contains(strings.ToLower(lastUser), "write file") {
		return Response{ToolCalls: []ToolCall{{
			ID:        "w1",
			Name:      "file_write",
			Arguments: `{"path":"demo.txt","content":"hello from orka"}`,
		}}}, nil
	}
	if !hasClarify && strings.Contains(strings.ToLower(lastUser), "clarify") {
		return Response{ToolCalls: []ToolCall{{
			ID:        "1",
			Name:      "clarify",
			Arguments: `{"question":"Which option do you want?","options":["A","B"]}`,
		}}}, nil
	}
	return Response{Content: "Echo: " + lastUser, FinishReason: "stop"}, nil
}

// ChatStream implements StreamingClient so the streaming path can be exercised
// offline: it computes the full Response via Chat, then replays the content as
// word-by-word deltas with a small delay to mimic a real token stream.
func (d DemoClient) ChatStream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	resp, err := d.Chat(ctx, req)
	if err != nil || resp.Content == "" || onDelta == nil {
		return resp, err
	}
	for _, w := range strings.SplitAfter(resp.Content, " ") {
		if w == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return resp, ctx.Err()
		case <-time.After(40 * time.Millisecond):
		}
		onDelta(w)
	}
	return resp, nil
}
