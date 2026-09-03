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
	// The consequence is spelled out because leaving the checklist stale is not
	// a cosmetic slip: this list IS the contract the run is graded against, and a
	// run that did all its work but never updated the plan is recorded as
	// incomplete. Measured here on a task that wrote the code, ran it, produced
	// the benchmark and the report, called update_plan once at the start and was
	// filed partial with all five steps still open.
	return "Declare and update your task checklist so the user can follow along. Call it once at the start of a multi-step task with all steps as \"pending\", then call it AGAIN EVERY TIME a step finishes — mark the step you are working on as \"active\" and finished steps as \"done\". Always pass the COMPLETE list of steps every time (it replaces the previous plan). " +
		"IMPORTANT: this checklist is what the run is judged by. If you finish the work but leave steps marked pending, the run is recorded as INCOMPLETE even though you did it — so update the plan as you go, and make sure every step is \"done\" before your final answer. Skip this tool entirely for trivial one-step requests. " +
		"While work remains, send it in the SAME batch of tool calls as that step's work rather than as a turn of its own, and do not re-send an unchanged checklist — a turn that only repeats the checklist costs a full model round-trip and moves nothing forward. " +
		"The ONE exception is the last update: before your final answer, call this tool once more with every step \"done\". That call has no work left to accompany it and is worth its round-trip, because without it a run that finished everything is still recorded as INCOMPLETE."
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
	if emit := agent.EmitFrom(ctx); emit != nil {
		emit(messages.Plan(plan, agent.MetaFrom(ctx)))
	}
	// Keep the latest checklist for the completion check at the end of the run:
	// the agent's own plan is the only contract we can hold a finished run to.
	planTrackerFrom(ctx).record(plan.Steps)
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
