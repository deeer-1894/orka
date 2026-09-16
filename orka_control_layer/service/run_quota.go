package service

import (
	"context"
	"fmt"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/messages"
)

// Scheduled failures retain their circuit breaker; token usage does not
// prevent a user from starting or continuing work.
const taskFailureLimit = 3

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
