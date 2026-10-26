package service

import (
	"context"
	"strings"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// Plan review: the user asked to approve the plan before the run changes
// anything. The first pass sees only read-only tools plus the plan tool; the
// moment it submits a plan the run pauses through the clarify machinery (same
// checkpoint, same resume), and the steps the user approves become the plan the
// resumed run is held to. Whether a plan is needed is the user's explicit
// switch, never a guess from the wording.

const (
	planReviewKind   = "plan_review"
	planReviewVar    = "plan_review" // rc.Vars marker: this checkpoint waits on a plan
	maxApprovedSteps = 30
)

const planReviewNotice = "[计划审阅] 用户要求先审阅计划再执行。本轮只有只读工具：需要时先做少量了解，然后调用 update_plan 列出完整步骤（全部 pending）和要交付的文件。提交计划后运行会暂停，等用户确认或修改；在此之前不要写文件、执行代码或操作浏览器。"

type planReviewKey struct{}

func withPlanReview(ctx context.Context) context.Context {
	return context.WithValue(ctx, planReviewKey{}, true)
}

func planReviewing(ctx context.Context) bool {
	on, _ := ctx.Value(planReviewKey{}).(bool)
	return on
}

// planReviewActive: the request asked for review and this is its first pass.
// Unattended runs have nobody to approve, so they never pause for a plan.
func planReviewActive(req ChatRunRequest) bool {
	return req.PlanReview && req.ResumeKey == "" && req.resumeFrom == nil && !unattendedRun(req.Trigger)
}

// planReviewTools keeps the read-only tools; the plan tool itself is added to
// every main agent by mainAgentTools.
func planReviewTools(tools []agent.BaseTool) []agent.BaseTool {
	// Interactive, with the user watching: read the workspace and the web,
	// change nothing.
	allowed := map[string]bool{}
	for _, name := range append(policyTools(db.TaskToolPolicy{}), capabilityTools[db.CapWorkspaceRead]...) {
		allowed[name] = true
	}
	out := make([]agent.BaseTool, 0, len(tools))
	for _, t := range tools {
		if t != nil && allowed[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

// returnDirectly is the set of tool calls that end the agent's turn: clarify
// always, and the plan tool while a plan waits for review.
func returnDirectly(ctx context.Context) map[string]bool {
	out := clarifyReturnDirectly()
	if planReviewing(ctx) {
		out[planToolName] = true
	}
	return out
}

// planReviewPause turns a submitted plan into the clarify-style pause.
func planReviewPause(rc *agent.RunContext) {
	rc.Messages = append(rc.Messages, messages.Chat(messages.RoleAssistant, "已提交执行计划，等待用户确认。", rc.Meta))
	rc.Put(planReviewVar, true)
	rc.Interrupt = &agent.Interrupt{Reason: "clarify", Clarify: &messages.ClarifyMessage{
		Kind:     planReviewKind,
		Question: "请确认执行计划：可以修改、增删或调整步骤顺序，确认后开始执行。",
		Options:  []string{"开始执行"},
	}}
}

// approvedPlan normalizes the steps the user confirmed: trimmed, de-duplicated,
// bounded, all pending.
func approvedPlan(titles []string) []messages.PlanStep {
	seen := map[string]bool{}
	var out []messages.PlanStep
	for _, t := range titles {
		t = strings.TrimSpace(t)
		if r := []rune(t); len(r) > 200 {
			t = string(r[:200])
		}
		if t == "" || seen[t] || len(out) == maxApprovedSteps {
			continue
		}
		seen[t] = true
		out = append(out, messages.PlanStep{Title: t, Status: "pending"})
	}
	return out
}

// applyApprovedPlan replaces the reviewed checkpoint's plan with the user's
// version and shows it, so the resumed run is measured against what was
// approved rather than what was proposed.
func applyApprovedPlan(rc *agent.RunContext, req ChatRunRequest, emit func(messages.Message)) {
	if pending, _ := rc.Vars[planReviewVar].(bool); !pending {
		return
	}
	delete(rc.Vars, planReviewVar)
	steps := approvedPlan(req.ApprovedPlan)
	if len(steps) == 0 {
		return
	}
	planTrackerFrom(rc.Ctx).restore(planCheckpoint{Steps: steps}, false)
	if emit != nil {
		emit(messages.Plan(messages.PlanUpdate{Steps: steps}, rc.Meta))
	}
}
