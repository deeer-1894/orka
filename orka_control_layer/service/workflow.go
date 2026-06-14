package service

import (
	"context"
	"time"

	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/db"
)

// RunWorkflow executes a workflow's steps as sequential turns in ONE fresh
// conversation: step N sees steps 1..N-1's output via conversation memory, so
// chaining is automatic. Each step is its own run record (trigger=workflow), and
// each retries per the (currently unused) headless retry path. Returns the
// conversation id so callers can open the run. Stops early on cancellation.
func (s *ChatService) RunWorkflow(ctx context.Context, wf db.Workflow, convID string) string {
	if s.Msg == nil || s.Msg.Store == nil || len(wf.Steps) == 0 {
		return ""
	}
	if convID == "" {
		convID = messages.NewID()
	}
	_ = s.Msg.Store.CreateConversation(ctx, &db.ConversationTable{
		ConversationID: convID,
		OwnerEmail:     wf.OwnerEmail,
		Title:          "流程 · " + wf.Name,
		TaskIds:        []string{},
		CreatedAt:      time.Now().UnixMilli(),
	})
	for _, step := range wf.Steps {
		if ctx.Err() != nil {
			return convID
		}
		s.RunHeadless(ctx, ChatRunRequest{
			Message:        step.Prompt,
			ConversationID: convID,
			UserEmail:      wf.OwnerEmail,
			Trigger:        "workflow",
		})
	}
	return convID
}
