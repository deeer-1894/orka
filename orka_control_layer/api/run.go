package api

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/service"
)

type listRunsReq struct {
	ConversationID string `json:"conversation_id"`
	TaskID         string `json:"task_id"`
	Status         string `json:"status"`
	Page           int64  `json:"page"`
	Size           int64  `json:"size"`
}

// ListRuns returns the authenticated user's execution history, newest-first,
// optionally filtered by conversation / parent task / status.
func (a *API) ListRuns(ctx context.Context, c *app.RequestContext) {
	var req listRunsReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	filter := bson.M{"owner_email": authEmail(c)}
	if req.ConversationID != "" {
		filter["conversation_id"] = req.ConversationID
	}
	if req.TaskID != "" {
		filter["task_id"] = req.TaskID
	}
	if req.Status != "" {
		filter["status"] = req.Status
	}
	runs, err := a.Store.ListRuns(ctx, filter, req.Page, req.Size)
	if err != nil {
		a.Log.Error("list runs", "err", err)
		fail(c, consts.StatusInternalServerError, "list failed")
		return
	}
	ok(c, map[string]any{"runs": runs})
}

type runIDReq struct {
	RunID string `json:"run_id"`
}

// GetRun returns one run (owner-scoped).
func (a *API) GetRun(ctx context.Context, c *app.RequestContext) {
	var req runIDReq
	if err := bind(c, &req); err != nil || req.RunID == "" {
		fail(c, consts.StatusBadRequest, "run_id required")
		return
	}
	run, err := a.Store.GetRun(ctx, req.RunID)
	if err != nil || run.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "run not found")
		return
	}
	ok(c, run)
}

// RerunRun re-executes a past run's prompt in its conversation as a fresh,
// detached background run (fire-and-forget — it shows up as a new entry in the
// run history; the user observes it there or opens the conversation).
func (a *API) RerunRun(ctx context.Context, c *app.RequestContext) {
	var req runIDReq
	if err := bind(c, &req); err != nil || req.RunID == "" {
		fail(c, consts.StatusBadRequest, "run_id required")
		return
	}
	run, err := a.Store.GetRun(ctx, req.RunID)
	if err != nil || run.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "run not found")
		return
	}
	go a.Chat.Run(context.Background(), service.ChatRunRequest{
		Message:        run.Prompt,
		ConversationID: run.ConversationID,
		TaskID:         run.TaskID,
		UserEmail:      run.OwnerEmail,
		Trigger:        "rerun",
	}, func(messages.Message) {})
	ok(c, map[string]string{"status": "rerunning"})
}
