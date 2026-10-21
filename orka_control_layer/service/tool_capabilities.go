package service

import (
	"context"
	"strings"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/modelprofile"
)

type quantCapabilityKey struct{}

// WithQuantCapability includes quant tools for a dedicated workflow with a restricted range.
func WithQuantCapability(ctx context.Context) context.Context {
	return context.WithValue(ctx, quantCapabilityKey{}, true)
}
func localCapabilityTools(ctx context.Context, req ChatRunRequest) []agent.BaseTool {
	tools := append(SkillTools(), ArtifactTools...)
	enabled, _ := ctx.Value(quantCapabilityKey{}).(bool)
	enabled = enabled || len(req.EnabledTools) == 0
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

// filterToolsForRequest applies the caller's declared tool whitelist and the
// selected model's capabilities. Tool choice within that set is the model's.
func filterToolsForRequest(ctx context.Context, tools []agent.BaseTool, req ChatRunRequest) []agent.BaseTool {
	tools = filterEnabled(tools, req.EnabledTools)
	if planReviewActive(req) {
		tools = planReviewTools(tools)
	}
	if selected, ok := modelprofile.FromContext(ctx); ok && selected.Capabilities.Vision {
		return tools
	}
	out := make([]agent.BaseTool, 0, len(tools))
	for _, tool := range tools {
		if tool != nil && tool.Name() != "run_agent" {
			out = append(out, tool)
		}
	}
	return out
}

func withRequestedExecutionScope(ctx context.Context, req ChatRunRequest) context.Context {
	enabled := len(req.EnabledTools) == 0
	for _, name := range req.EnabledTools {
		if name == "code" || name == "python" || name == "shell" {
			enabled = true
		}
	}
	return context.WithValue(ctx, executionScopeKey{}, enabled && !catalogOnly(ctx))
}
func requestedExecutionScope(ctx context.Context) bool {
	enabled, _ := ctx.Value(executionScopeKey{}).(bool)
	return enabled
}
