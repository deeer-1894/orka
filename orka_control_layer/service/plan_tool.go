package service

import (
	"context"
	"encoding/json"
	"reflect"
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
	return "Maintain the task checklist for larger work. Declare all steps (pending) and every required output file up front; give each step a short stable id and keep it when renaming. " +
		"Update status as work happens: pending/active/done/blocked. Omitted steps stay outstanding, and outputs are additive. " +
		"Mark a step done only after its work and checks succeeded — never to clear the list; mark what cannot be done blocked with the concrete limitation and continue independent work. " +
		"A fallback source does not complete a step that required interacting with a browser page; after a failed browser action cite later successful receipt ids in evidence_ids and say what was observed in reason. " +
		"Send each update in the same response as real work, never alone. For requested files call check_delivery; run tests only for software or computed data. Read the returned unfinished fields."
}
func (planTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"final_response": map[string]any{"type": "string", "enum": []string{"answer", "file_receipt"}, "description": "answer (default): a normal chat answer. file_receipt: the user wanted files plus links and a brief acceptance, with all findings and limitations inside the files; the runtime then publishes the links."},
			"outputs":        map[string]any{"type": "array", "maxItems": 128, "description": "Every required output file (workspace-relative); additive across updates.", "items": map[string]any{"type": "string"}},
			"steps": map[string]any{
				"type":        "array",
				"description": "the full ordered checklist",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"reason":       map[string]any{"type": "string", "maxLength": 1200, "description": "Observed result supporting done, or the concrete blocker."},
						"evidence_ids": map[string]any{"type": "array", "maxItems": 32, "items": map[string]any{"type": "string"}, "description": "Successful browser receipt ids observed after this step's failed actions."},
						"id":           map[string]any{"type": "string", "description": "stable identifier; keep unchanged when renaming this step"},
						"title":        map[string]any{"type": "string", "description": "short imperative step description"},
						"status":       map[string]any{"type": "string", "enum": []string{"pending", "active", "done", "blocked"}, "description": "pending | active | blocked | done"},
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
		before := tracker.snapshot()
		tracker.record(submitted)
		changed = !reflect.DeepEqual(before, tracker.snapshot())
		if !changed {
			note = "本次更新没有改变实际状态。请处理返回的步骤原因；不要重复提交 done 或改标题绕过验证。"
		}
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
	recovery := tracker.browserRecoveryNeeds()
	if len(recovery) > 0 {
		note += " browser_recovery 列出每次未核实操作及已取得的恢复回执。先核对这些现有观察，在对应步骤 evidence_ids 中引用可用回执，并说明实际结果；不要重复浏览已经核实的页面。"
	}
	b, err := json.Marshal(struct {
		Changed           bool                  `json:"changed"`
		Steps             []messages.PlanStep   `json:"steps"`
		Unfinished        []string              `json:"unfinished"`
		OmittedUnfinished []string              `json:"omitted_unfinished"`
		Note              string                `json:"note"`
		BrowserRecovery   []browserRecoveryNeed `json:"browser_recovery,omitempty"`
	}{changed, plan.Steps, unfinished, omitted, note, recovery})
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
		case "blocked":
			status = "blocked"
		case "done", "completed", "complete", "finished":
			status = "done"
		default:
			status = "pending"
		}
		id, _ := m["id"].(string)
		id = strings.TrimSpace(id)
		reason, _ := m["reason"].(string)
		var evidenceIDs []string
		if ids, ok := m["evidence_ids"].([]any); ok {
			for _, v := range ids {
				if id, ok := v.(string); ok && len(evidenceIDs) < 32 {
					evidenceIDs = append(evidenceIDs, id)
				}
			}
		}
		p.Steps = append(p.Steps, messages.PlanStep{ID: id, Title: title, Status: status, Reason: trunc(strings.TrimSpace(reason), 1200), EvidenceIDs: evidenceIDs})
	}
	return p
}

// withPlan appends the plan tool to a tool set (orchestrator / main agent only —
// sub-agents don't own the user-facing checklist).
func withPlan(tools []agent.BaseTool) []agent.BaseTool {
	return append(tools, planTool{}, deliveryCheckTool{}, acceptanceCheckTool{})
}
