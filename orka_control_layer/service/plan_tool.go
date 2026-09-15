package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// planTool lets the agent declare and maintain a task checklist as FIRST-CLASS
// structured state (an EventPlan), instead of the UI having to regex a numbered
// list out of the prose. The agent calls it once up front with every step
// "pending", then calls it again as work progresses to flip a step to "active"
// (currently working) or "done". Updates merge by exact title: omitted steps
// remain obligations. Both the model response and UI show the merged plan.
// It never touches the workspace, so it is not a danger tool and is not gated.
type planTool struct{}

const planToolName = "update_plan"

func (planTool) Name() string { return planToolName }
func (planTool) Description() string {
	return "Maintain the task checklist. For multi-step work declare all pending steps and all required output file paths up front. Give every multi-step item a short stable id and keep that id unchanged while updating its title or status. Update actual progress with pending/active/done; omitted steps remain outstanding. For legacy title-only items, keep the title stable; renaming without an id is treated as a new obligation. Read the returned canonical steps, unfinished and omitted_unfinished fields. After verifying the original work, explicitly update its original title; never mark it done merely to clear the checklist. Output requirements are additive and cannot be removed by rewriting the plan. Only mark a step done after its work and relevant checks succeed. For file deliveries call check_delivery and task-specific tests before finishing. Include plan updates with actual work when possible; do not repeat unchanged plans. Research and unrelated computation can progress independently: generate working artifacts early and reserve time and tokens for verification, report, README and packaging."

}
func (planTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"final_response": map[string]any{"type": "string", "enum": []string{"answer", "file_receipt"}, "description": "Default answer preserves the normal chat answer. Choose file_receipt only when the user requests files plus download links and brief acceptance, with substantive findings and ALL limitations in the files, and does not require a separate substantive chat answer. Once files pass checks and all original steps are done, the runtime publishes file links and structural check scope; do not regenerate statistics in chat. Omission preserves the previous mode."},
			"outputs":        map[string]any{"type": "array", "maxItems": 128, "description": "All required workspace-relative output file paths, including reports, README, manifest and archive when requested. Additive across updates.", "items": map[string]any{"type": "string"}},
			"steps": map[string]any{
				"type":        "array",
				"description": "the full ordered checklist",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":     map[string]any{"type": "string", "description": "stable identifier; keep unchanged when renaming this step"},
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

// Invoke publishes and returns the same authoritative plan. Returning only an
// acknowledgement hid omitted originals from the model after a rename or split.
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
	mode, _ := args["final_response"].(string)
	if err := deliveryFrom(ctx).configure(outputs, mode); err != nil {
		return "", err
	}
	tracker := planTrackerFrom(ctx)
	submitted := plan.Steps
	changed := len(submitted) > 0 && !tracker.same(submitted)
	note := "已更新任务清单。仅在实际工作及相关验证完成后将对应原始步骤标记为 done。"
	if tracker.completed() && changed {
		changed = false
		note = "当前计划已全部完成，本轮不再追加新的清单。若用户提出新目标，请开始新的任务会话。"
	} else if changed {
		tracker.record(submitted)
	} else {
		note = "计划未变化。不要重复提交相同清单；继续实际工作，并核对下面保留的未完成步骤。"
	}
	if tracker != nil {
		plan.Steps = tracker.snapshot()
	}
	if changed {
		if emit := agent.EmitFrom(ctx); emit != nil {
			emit(messages.Plan(plan, agent.MetaFrom(ctx)))
		}
	}
	submittedKeys := make(map[string]bool, len(submitted))
	for _, step := range submitted {
		submittedKeys[planStepKey(step)] = true
	}
	unfinished, omitted := []string{}, []string{}
	for _, step := range plan.Steps {
		if step.Status != "done" {
			unfinished = append(unfinished, step.Title)
			if !submittedKeys[planStepKey(step)] {
				omitted = append(omitted, step.Title)
			}
		}
	}
	if len(omitted) > 0 {
		note += " 本次遗漏的原始步骤仍未完成；改名或拆分不会替代它们。请核对原始要求与实际证据，再按原始标题更新状态，不要直接清空或批量勾选。"
	}
	b, err := json.Marshal(struct {
		Changed           bool                `json:"changed"`
		Steps             []messages.PlanStep `json:"steps"`
		Unfinished        []string            `json:"unfinished"`
		OmittedUnfinished []string            `json:"omitted_unfinished"`
		Note              string              `json:"note"`
	}{changed, plan.Steps, unfinished, omitted, note})
	return string(b), err
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
		id, _ := m["id"].(string)
		id = strings.TrimSpace(id)
		p.Steps = append(p.Steps, messages.PlanStep{ID: id, Title: title, Status: status})
	}
	return p
}

// withPlan appends the plan tool to a tool set (orchestrator / main agent only —
// sub-agents don't own the user-facing checklist).
func withPlan(tools []agent.BaseTool) []agent.BaseTool {
	return append(tools, planTool{}, deliveryCheckTool{})
}
