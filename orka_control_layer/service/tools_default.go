package service

import (
	"context"
	"errors"
)

// An unconfigured executor must never return simulated completion in production.
type guiUnavailableTool struct{}

func (guiUnavailableTool) Name() string { return "run_agent" }
func (guiUnavailableTool) Description() string {
	return "GUI execution is unavailable: configure the GUI service before requesting browser actions."
}
func (guiUnavailableTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"instruction": map[string]any{"type": "string"}}, "required": []string{"instruction"}}
}
func (guiUnavailableTool) Invoke(context.Context, map[string]any) (string, error) {
	return "", errors.New("GUI executor is not configured; no browser action was performed")
}
