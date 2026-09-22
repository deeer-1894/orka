package service

import (
	"context"

	"github.com/orka-oss/orka_core/agent"
)

// mainAgentTools adds only the control tools that make sense for this run. The
// execution policy has already removed forbidden capabilities from base.
func mainAgentTools(ctx context.Context, base []agent.BaseTool) []agent.BaseTool {
	policy := executionPolicyFrom(ctx)
	out := append([]agent.BaseTool(nil), base...)
	if policy.NeedsPlan {
		out = append(out, planTool{})
	}
	out = append(out, clarifyTool{})
	if !policy.SourceVerificationOnly {
		out = append(out, deliveryCheckTool{}, acceptanceCheckTool{})
	}
	if !policy.StrictSources && !policy.SourceVerificationOnly {
		out = append(out, findTools{})
	}
	return out
}

// workerAgentTools adds progressive discovery only when the worker catalog can
// actually contain hidden capabilities. Strict browser runs do not use workers,
// but keeping this rule here prevents future callers from reopening that catalog.
func workerAgentTools(ctx context.Context, base []agent.BaseTool) []agent.BaseTool {
	out := append([]agent.BaseTool(nil), base...)
	if !executionPolicyFrom(ctx).StrictSources {
		out = append(out, findTools{})
	}
	return out
}
