package service

import (
	"context"
	"fmt"
)

// guiMockTool stands in for the GUI agent (run_agent) until Phase 6 connects the
// real Playwright/CDP executor over WebSocket.
type guiMockTool struct{}

func (guiMockTool) Name() string { return "run_agent" }
func (guiMockTool) Description() string {
	return "Run a GUI automation task in a browser (mock until Phase 6). Input: a natural-language instruction."
}
func (guiMockTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"instruction": map[string]any{"type": "string"}},
		"required":   []string{"instruction"},
	}
}
func (guiMockTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	return fmt.Sprintf("[gui-mock] completed task: %v", args["instruction"]), nil
}
