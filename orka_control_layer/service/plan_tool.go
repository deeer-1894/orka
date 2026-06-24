package service

import (
	"context"
	"strings"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// planTool lets the agent declare and maintain a task checklist as FIRST-CLASS
// structured state (an EventPlan), instead of the UI having to regex a numbered
// list out of the prose. The agent calls it once up front with every step
// "pending", then calls it again as work progresses to flip a step to "active"
// (currently working) or "done". Each call is an idempotent snapshot of the
// whole plan; the UI renders the latest one as a live progress checklist.
//
// It is a pure UI side-channel tool: it emits a plan event and returns, never
// touching the workspace, so it is not a danger tool and is not gated.
type planTool struct{}

const planToolName = "update_plan"

func (planTool) Name() string { return planToolName }
func (planTool) Description() string {
	return "Declare and update your task checklist so the user can follow along. Call it once at the start of a multi-step task with all steps as \"pending\", then call it again whenever progress changes — mark the step you are working on as \"active\" and finished steps as \"done\". Always pass the COMPLETE list of steps every time (it replaces the previous plan). Skip it for trivial one-step requests."
}
func (planTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"steps": map[string]any{
				"type":        "array",
				"description": "the full ordered checklist",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":  map[string]any{"type": "string", "description": "short imperative step description"},
						"status": map[string]any{"type": "string", "enum": []string{"pending", "active", "done"}, "description": "pending | active | done"},
					},
					"required": []string{"title", "status"},
				},
			},
		},
		"required": []string{"steps"},
	}
}

// Invoke parses the steps, emits an EventPlan snapshot to the UI side-channel,
// and returns a short acknowledgement so the agent keeps going.
func (planTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	plan := planFromArgs(args)
	if len(plan.Steps) == 0 {
		return "计划为空,已忽略。", nil
	}
	if emit := agent.EmitFrom(ctx); emit != nil {
		emit(messages.Plan(plan, agent.MetaFrom(ctx)))
	}
	return "已更新任务清单。", nil
}

// planFromArgs normalizes the loosely-typed tool args into a PlanUpdate.
func planFromArgs(args map[string]any) messages.PlanUpdate {
	var p messages.PlanUpdate
	raw, ok := args["steps"].([]any)
	if !ok {
		return p
	}
	for _, it := range raw {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		title, _ := m["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		status, _ := m["status"].(string)
		status = strings.ToLower(strings.TrimSpace(status))
		switch status {
		case "active", "in_progress", "doing", "current":
			status = "active"
		case "done", "completed", "complete", "finished":
			status = "done"
		default:
			status = "pending"
		}
		p.Steps = append(p.Steps, messages.PlanStep{Title: title, Status: status})
	}
	return p
}

// withPlan appends the plan tool to a tool set (orchestrator / main agent only —
// sub-agents don't own the user-facing checklist).
func withPlan(tools []agent.BaseTool) []agent.BaseTool {
	return append(tools, planTool{})
}
