package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// Progress is an acknowledged tool operation, not a model claim, a plan update,
// token spend, or proof that the output satisfies the task's business rules.
type progressTool struct {
	agent.BaseTool
	budget *runBudget
}

func (t progressTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	out, err := t.BaseTool.Invoke(ctx, args)
	if err == nil && ctx.Err() == nil && usefulToolResult(t.Name(), out) && t.budget != nil {
		t.budget.mu.Lock()
		t.budget.successfulTools++
		t.budget.mu.Unlock()
	}
	return out, err
}
func trackToolProgress(tools []agent.BaseTool, b *runBudget) []agent.BaseTool {
	out := make([]agent.BaseTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, progressTool{BaseTool: t, budget: b})
	}
	return out
}
func usefulToolResult(name, out string) bool {
	switch name {
	case "", "update_plan", "find_tools", "clarify", "apply_skill", "task":
		return false
	}
	// Confirmation gates acknowledge a refusal without invoking the operation.
	// Their nil error is control flow, not completed work (also in old journals).
	switch strings.TrimSpace(out) {
	case "用户拒绝了该操作,已跳过。", "未收到确认结果,已跳过该操作。", "等待用户确认超时,已跳过该操作。":
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(out))
	if strings.HasPrefix(lower, "tool call failed") || strings.HasPrefix(lower, "tool error (") || strings.HasPrefix(lower, "[recovery: outcome unknown]") {
		return false
	}
	var result map[string]any
	// Tool adapters append diagnostics to the original result. Decode its first
	// JSON value so an acknowledged failure cannot turn into success merely
	// because the complete decorated text is no longer a JSON document.
	if json.NewDecoder(strings.NewReader(out)).Decode(&result) == nil {
		if result["isError"] == true || result["is_error"] == true || result["success"] == false || result["ok"] == false {
			return false
		}
		if e, ok := result["error"]; ok && e != nil && e != "" {
			return false
		}
		if code, ok := result["exit_code"].(float64); ok && code != 0 {
			return false
		}
	}
	return true
}
func (b *runBudget) toolProgress() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.successfulTools
}

type runOutcome struct {
	status, errorDetail, budgetHit string
	unfinished                     []string
}

// Separate terminal classification from persistence so partial/error/accounting
// fields and cancellation precedence can be tested without a database.
func assessRunOutcome(rc *agent.RunContext, runErr, ctxErr error) runOutcome {
	out := runOutcome{status: db.RunDone}
	if rc.Ctx != nil {
		out.budgetHit = budgetFrom(rc.Ctx).exhausted()
		out.unfinished = planTrackerFrom(rc.Ctx).unfinished()
	}
	switch {
	case errors.Is(runErr, context.Canceled) || errors.Is(ctxErr, context.Canceled):
		out.status, out.errorDetail = db.RunFailed, "cancelled"
	case runErr != nil:
		out.status, out.errorDetail = db.RunFailed, runErr.Error()
		if llm.IsCallLimit(runErr) && rc.Ctx != nil {
			b := budgetFrom(rc.Ctx)
			if b != nil {
				b.mu.Lock()
				b.lastCallError = runErr.Error()
				b.mu.Unlock()
			}
			if b.toolProgress() > 0 {
				out.status = db.RunPartial
			}
			out.unfinished = append(out.unfinished, "模型单次调用中断；剩余原始要求和验收尚未确认")
		}
	case rc.Interrupt != nil:
		out.status = db.RunPaused
	}
	if rc.Ctx != nil && (out.status == db.RunDone || out.status == db.RunPartial) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out.unfinished = append(out.unfinished, deliveryFrom(rc.Ctx).failures(ctx)...)
		cancel()
		if out.status == db.RunDone && (out.budgetHit != "" || len(out.unfinished) > 0) {
			out.status = db.RunPartial
		}
	}
	// Delivery assessment may block behind a concurrent inspection. Its entry
	// cancellation snapshot is no longer authoritative after that wait.
	if rc.Ctx != nil && errors.Is(rc.Ctx.Err(), context.Canceled) {
		out.status, out.errorDetail = db.RunFailed, "cancelled"
	}
	return out
}

func callLimitNotice(out runOutcome) string {
	text := "模型单次调用达到输出或时间限额，本轮已停止，不会自动重试。"
	if out.status == db.RunPartial {
		text += "已记录实际工具进度，已有成果不会被本次限额处理删除；这不代表成果已验证或任务完成。恢复应使用保留的执行记录和剩余累计预算，从一个小步骤继续，不从头重做。"
	} else {
		text += "尚无可确认的实际工具进度，本轮按失败记录；未声称产物已生成或已验证。"
	}
	if out.budgetHit != "" {
		text += "当前任务预算已触限（" + out.budgetHit + "），不会重置额度续跑。"
	}
	return text + "\n未完成事项：\n- " + strings.Join(out.unfinished, "\n- ")
}

// Older journals predate the explicit progress counter. Only matched, recorded
// tool outcomes can seed it; summaries, plan claims and synthetic recovery
// placeholders are deliberately insufficient.
func restoreToolProgress(b *runBudget, rr *runResume) {
	if b == nil || rr == nil || b.toolProgress() > 0 {
		return
	}
	if c := rr.Checkpoint; c != nil && (c.successfulToolsRecorded || c.SuccessfulTools > 0) {
		// The runtime's measured zero is authoritative. Decorated transcript
		// text cannot override it; inference exists only for legacy checkpoints.
		return
	}
	count := acknowledgedTools(rr.Messages)
	byAgent := map[string][]*schema.Message{}
	for _, record := range rr.Delegates {
		byAgent[record.Agent] = append(byAgent[record.Agent], record.Message)
	}
	for _, msgs := range byAgent {
		count += acknowledgedTools(msgs)
	}
	b.mu.Lock()
	b.successfulTools = max(b.successfulTools, count)
	b.mu.Unlock()
}
func acknowledgedTools(msgs []*schema.Message) int {
	calls := map[string]string{}
	count := 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if m.Role == schema.Assistant {
			for _, tc := range m.ToolCalls {
				calls[tc.ID] = tc.Function.Name
			}
		}
		if m.Role == schema.Tool {
			if name := calls[m.ToolCallID]; usefulToolResult(name, m.Content) {
				count++
			}
			delete(calls, m.ToolCallID)
		}
	}
	return count
}

const varPublishedCallLimitOutcome = "published_call_limit_outcome"

// This is the terminal decision boundary. Cancellation wins until publication;
// after publication finalization persists this same snapshot rather than
// reclassifying an already terminal run because a late cancellation arrived.
func (s *ChatService) publishCallLimitOutcome(ctx context.Context, rc *agent.RunContext, meta messages.Meta, raw func(messages.Message), out runOutcome) {
	if out.errorDetail == "cancelled" || errors.Is(ctx.Err(), context.Canceled) || (rc.Ctx != nil && errors.Is(rc.Ctx.Err(), context.Canceled)) {
		out.status, out.errorDetail = db.RunFailed, "cancelled"
		middlewares.SetFinal(rc, "本轮已由用户取消；已有成果不代表已完成验收。")
	}
	rc.Put(varPublishedCallLimitOutcome, out)
	event := messages.Task(out.status, meta)
	event.Content = out.errorDetail
	s.Msg.Deliver(rc, raw, event, true)
}
