package api

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/db"
)

// ListWorkflows returns the user's defined workflows.
func (a *API) ListWorkflows(ctx context.Context, c *app.RequestContext) {
	wfs, err := a.Store.ListWorkflows(ctx, authEmail(c))
	if err != nil {
		fail(c, consts.StatusInternalServerError, "list failed")
		return
	}
	ok(c, map[string]any{"workflows": wfs})
}

type createWorkflowReq struct {
	Name  string            `json:"name"`
	Steps []db.WorkflowStep `json:"steps"`
}

// CreateWorkflow saves a workflow definition.
func (a *API) CreateWorkflow(ctx context.Context, c *app.RequestContext) {
	var req createWorkflowReq
	if err := bind(c, &req); err != nil || req.Name == "" || len(req.Steps) == 0 {
		fail(c, consts.StatusBadRequest, "name and at least one step required")
		return
	}
	wf := &db.Workflow{
		WorkflowID: "wf_" + messages.NewID(),
		OwnerEmail: authEmail(c),
		Name:       req.Name,
		Steps:      req.Steps,
		CreatedAt:  time.Now().UnixMilli(),
	}
	if err := a.Store.CreateWorkflow(ctx, wf); err != nil {
		fail(c, consts.StatusInternalServerError, "create failed")
		return
	}
	ok(c, wf)
}

type workflowIDReq struct {
	WorkflowID string `json:"workflow_id"`
}

// DeleteWorkflow removes a workflow.
func (a *API) DeleteWorkflow(ctx context.Context, c *app.RequestContext) {
	var req workflowIDReq
	if err := bind(c, &req); err != nil || req.WorkflowID == "" {
		fail(c, consts.StatusBadRequest, "workflow_id required")
		return
	}
	if err := a.Store.DeleteWorkflow(ctx, req.WorkflowID, authEmail(c)); err != nil {
		fail(c, consts.StatusInternalServerError, "delete failed")
		return
	}
	ok(c, map[string]string{"status": "deleted"})
}

// RunWorkflow kicks off a workflow as a detached sequential run and returns the
// conversation id (observe its steps there + in run history).
func (a *API) RunWorkflow(ctx context.Context, c *app.RequestContext) {
	var req workflowIDReq
	if err := bind(c, &req); err != nil || req.WorkflowID == "" {
		fail(c, consts.StatusBadRequest, "workflow_id required")
		return
	}
	wf, err := a.Store.GetWorkflow(ctx, req.WorkflowID)
	if err != nil || wf.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "workflow not found")
		return
	}
	convID := messages.NewID()
	go a.Chat.RunWorkflow(context.Background(), *wf, convID)
	ok(c, map[string]string{"status": "running", "conversation_id": convID})
}
