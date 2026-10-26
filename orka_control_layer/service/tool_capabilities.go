package service

import (
	"context"
	"os"
	"strings"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/modelprofile"
)

type quantCapabilityKey struct{}

// QuantEnabled reports whether the deployment opted into the quant tools and
// API (ORKA_QUANT=1).
func QuantEnabled() bool { return os.Getenv("ORKA_QUANT") == "1" }

// WorkflowsEnabled reports whether the deployment opted into the workflow API
// (ORKA_WORKFLOWS=1); the web app no longer has a screen for it.
func WorkflowsEnabled() bool { return os.Getenv("ORKA_WORKFLOWS") == "1" }

// WithQuantCapability includes quant tools for a dedicated workflow with a restricted range.
func WithQuantCapability(ctx context.Context) context.Context {
	return context.WithValue(ctx, quantCapabilityKey{}, true)
}
func localCapabilityTools(ctx context.Context, req ChatRunRequest) []agent.BaseTool {
	tools := append(SkillTools(), ArtifactTools...)
	// Quant tools are off unless the deployment opts in (ORKA_QUANT=1) or a
	// dedicated pipeline asks: their backtests are not trustworthy enough to
	// hand an agent by default (look-ahead, unevaluated expressions, random
	// metrics for unknown ones) and no review screen gates them any more.
	pipeline, _ := ctx.Value(quantCapabilityKey{}).(bool)
	enabled := QuantEnabled() && len(req.EnabledTools) == 0
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
	if pipeline || (enabled && QuantEnabled()) {
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
