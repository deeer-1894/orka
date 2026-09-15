package service

import (
	"context"
	"github.com/orka-oss/orka_core/agent"
	"strings"
)

type quantCapabilityKey struct{}

// WithQuantCapability is reserved for an explicitly requested quant workflow.
func WithQuantCapability(ctx context.Context) context.Context {
	return context.WithValue(ctx, quantCapabilityKey{}, true)
}
func localCapabilityTools(ctx context.Context, req ChatRunRequest) []agent.BaseTool {
	tools := append(SkillTools(), ArtifactTools...)
	enabled, _ := ctx.Value(quantCapabilityKey{}).(bool)
	for _, name := range req.EnabledTools {
		if name == "quant" {
			enabled = true
		}
		for _, t := range QuantTools {
			if name == t.Name() {
				enabled = true
			}
		}
	}
	if enabled {
		tools = append(tools, QuantTools...)
	}
	return tools
}
func dangerousToolName(name string) bool { return dangerTools[name] || strings.HasPrefix(name, "mcp_") }

type executionScopeKey struct{}

func withRequestedExecutionScope(ctx context.Context, req ChatRunRequest) context.Context {
	enabled := false
	for _, name := range req.EnabledTools {
		if name == "code" || name == "python" || name == "shell" {
			enabled = true
		}
	}
	return context.WithValue(ctx, executionScopeKey{}, enabled)
}
func requestedExecutionScope(ctx context.Context) bool {
	enabled, _ := ctx.Value(executionScopeKey{}).(bool)
	return enabled
}
