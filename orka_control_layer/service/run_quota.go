package service

import (
	"context"
	"fmt"
	"time"

	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/db"
)

// Cost guardrails. The per-run budget in run_budget.go stops one execution from
// running away; this file stops the OTHER failure mode, which is many executions
// each individually reasonable. A scheduled task that fails and retries on every
// tick, or a loop that keeps re-asking, spends real money with nobody watching —
// on this deployment a single run reached 627k tokens and nothing anywhere would
// have objected.

const (
	// runMaxTokens caps ONE execution. Set well above normal work (measured p90
	// here is ~144k) so it never interferes with a legitimately large job — it is
	// a backstop against runaway loops, not a performance budget.
	runMaxTokens = 800_000
	// runMaxWall caps one execution's wall clock. Long research legitimately
	// takes tens of minutes; this only catches a run that has stopped making
	// progress at all.
	runMaxWall = 2 * time.Hour
	// userDailyTokens caps a single user's rolling 24h spend across all runs.
	userDailyTokens = 5_000_000
	// taskFailureLimit is how many consecutive failures disable a scheduled task.
	// Three distinguishes a persistent fault from a transient one — a flaky
	// network or a rate limit rarely repeats three times running.
	taskFailureLimit = 3
)

// quotaExceeded reports why a user may not start a new run, or "" to proceed.
// Checked before the run starts: refusing up front costs nothing, whereas
// discovering the ceiling mid-run wastes everything spent getting there.
func (s *ChatService) quotaExceeded(ctx context.Context, email string) string {
	if email == "" || s.Msg == nil || s.Msg.Store == nil {
		return ""
	}
	since := time.Now().Add(-24 * time.Hour).UnixMilli()
	used, err := s.Msg.Store.TokensSince(ctx, email, since)
	if err != nil {
		return "" // never block work because the meter is unreadable
	}
	if used < userDailyTokens {
		return ""
	}
	return fmt.Sprintf("已达到 24 小时用量上限(%s / %s token)。请稍后再试,或在配置中调高上限。",
		humanCount(used), humanCount(userDailyTokens))
}

// recordTaskOutcome advances a scheduled task's circuit breaker and trips it
// after taskFailureLimit consecutive failures. An unattended task that cannot
// succeed is not worth retrying forever; stopping it and saying so is strictly
// better than silently burning the user's quota every tick.
func (s *ChatService) recordTaskOutcome(ctx context.Context, taskID, email string, ok bool) {
	if taskID == "" || s.Msg == nil || s.Msg.Store == nil {
		return
	}
	fails, err := s.Msg.Store.RecordTaskOutcome(ctx, taskID, ok)
	if err != nil || ok || fails < taskFailureLimit {
		return
	}
	reason := fmt.Sprintf("连续 %d 次运行失败,已自动停用", fails)
	if err := s.Msg.Store.DisableTask(ctx, taskID, reason); err != nil {
		return
	}
	if s.Log != nil {
		s.Log.Warn("task circuit breaker tripped", "task_id", taskID, "fails", fails)
	}
	if email == "" {
		return
	}
	_ = s.Msg.Store.CreateNotification(ctx, &db.Notification{
		NotificationID: "ntf_" + messages.NewID(),
		OwnerEmail:     email,
		Kind:           "task_disabled",
		Title:          "自动任务已停用",
		Body:           reason + "。修复后可在任务页重新启用。",
		CreatedAt:      time.Now().UnixMilli(),
	})
	if s.OnEvent != nil {
		s.OnEvent(email, "notification")
	}
}

// humanCount renders a token count compactly (1234567 → "1.2M").
func humanCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}
