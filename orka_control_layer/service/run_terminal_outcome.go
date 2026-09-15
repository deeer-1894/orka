package service

import (
	"context"
	"errors"
	"strings"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

const varPublishedRunOutcome = "published_run_outcome"

// Publication is the terminal decision boundary for normal completion as well
// as failure. Cancellation wins until then; finalization persists this exact
// snapshot, including pending obligations, instead of assessing it a second time.
func (s *ChatService) publishRunOutcome(ctx context.Context, rc *agent.RunContext, meta messages.Meta, raw func(messages.Message), out runOutcome) {
	if out.errorDetail == "cancelled" || errors.Is(ctx.Err(), context.Canceled) || (rc.Ctx != nil && errors.Is(rc.Ctx.Err(), context.Canceled)) {
		out.status, out.errorDetail = db.RunFailed, "cancelled"
		middlewares.SetFinal(rc, "本轮已由用户取消；已有成果不代表已完成验收。")
	}
	if out.status == db.RunDone && rc.Ctx != nil {
		if err := s.publishDelivery(rc.Ctx); err != nil {
			out.status = db.RunPartial
			out.unfinished = append(out.unfinished, "固定交付版本未能发布："+err.Error())
			out.errorDetail = "delivery publication failed"
			notice := partialRunNotice(out)
			middlewares.SetFinal(rc, notice)
			s.Msg.Deliver(rc, raw, messages.Chat(messages.RoleAssistant, notice, meta), true)
		}
	}
	if rc.Ctx != nil && errors.Is(rc.Ctx.Err(), context.Canceled) {
		out.status, out.errorDetail = db.RunFailed, "cancelled"
	}
	if rc.Ctx != nil {
		if session := BudgetSessionFrom(rc.Ctx); session != nil {
			runID := meta.RunID
			if runID == "" {
				runID = session.runID
			}
			if err := session.PersistRun(ctx, runID, out.status); err != nil {
				out.status = db.RunPartial
				out.errorDetail = "budget snapshot persistence failed"
				out.unfinished = append(out.unfinished, "任务用量快照未能保存；已结算费用与未完成预留仍在账本中")
				notice := partialRunNotice(out)
				middlewares.SetFinal(rc, notice)
				s.Msg.Deliver(rc, raw, messages.Chat(messages.RoleAssistant, notice, meta), true)
				if s.Log != nil {
					s.Log.Error("run budget snapshot failed", "run_id", runID, "err", err)
				}
			}
		}
	}
	rc.Put(varPublishedRunOutcome, out)
	event := messages.Task(out.status, meta)
	event.Content = out.errorDetail
	s.Msg.Deliver(rc, raw, event, true)
}

func partialRunNotice(out runOutcome) string {
	text := "本轮已停止，但仍有未完成或未确认事项，暂不能确认全部完成。已有成果保留。请逐项核对原始要求与实际证据；改名、拆分或遗漏计划步骤不代表完成。"
	if out.budgetHit != "" {
		text += "任务预算已触限（" + out.budgetHit + "）。"
	}
	if len(out.unfinished) > 0 {
		text += "\n未完成事项：\n- " + strings.Join(out.unfinished, "\n- ")
	}
	return text
}
