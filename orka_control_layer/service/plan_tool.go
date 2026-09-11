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
	return "Maintain the task checklist. For multi-step work declare all pending steps and all required output file paths up front. Keep step titles stable and update actual progress with pending/active/done; omitted steps remain outstanding. Output requirements are additive and cannot be removed by rewriting the plan. Only mark a step done after its work and relevant checks succeed. For file deliveries call check_delivery and task-specific tests before finishing. Include plan updates with actual work when possible; do not repeat unchanged plans. Research and unrelated computation can progress independently: generate working artifacts early and reserve time and tokens for verification, report, README and packaging."

}
func (planTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"outputs": map[string]any{"type": "array", "maxItems": 128, "description": "All required workspace-relative output file paths, including reports, README, manifest and archive when requested. Additive across updates.", "items": map[string]any{"type": "string"}},
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
	var outputs []string
	if raw, ok := args["outputs"].([]any); ok {
		for _, v := range raw {
			if p, ok := v.(string); ok {
				outputs = append(outputs, p)
			}
		}
	}
	if err := deliveryFrom(ctx).declare(outputs); err != nil {
		return "", err
	}
	if len(plan.Steps) == 0 {
		return "计划为空,已忽略。", nil
	}
	// A re-post of the identical checklist carries no information, and the model
	// does it: consecutive update_plan calls 41-80 seconds apart were measured
	// here, each one a whole model round-trip that moved no work forward. Say so
	// rather than acknowledging it, so the feedback lands where the decision is
	// made. The event is still suppressed, not the record — an unchanged plan is
	// by definition already recorded.
	if planTrackerFrom(ctx).same(plan.Steps) {
		return "计划与上次完全相同,未做改动。不要为了汇报进度单独调用本工具 —— " +
			"只在计划真正变化时调用,并把它和这一轮的实际工作放在同一批工具调用里。", nil
	}
	planTrackerFrom(ctx).record(plan.Steps)
	if tracker := planTrackerFrom(ctx); tracker != nil {
		plan.Steps = tracker.snapshot()
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
	return append(tools, planTool{}, deliveryCheckTool{})
}
