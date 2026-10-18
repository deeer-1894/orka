package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/service"
	workflowstate "github.com/orka-oss/orka_control_layer/workflow"
	"github.com/orka-oss/orka_core/messages"
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
	if err := bind(c, &req); err != nil || strings.TrimSpace(req.Name) == "" || len(req.Steps) == 0 {
		fail(c, consts.StatusBadRequest, "name and at least one step required")
		return
	}
	if err := workflowstate.Validate(req.Steps); err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
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

// RunWorkflow durably admits a detached DAG and returns its parent run ID.
func (a *API) RunWorkflow(ctx context.Context, c *app.RequestContext) {
	var req workflowIDReq
	if err := bind(c, &req); err != nil || req.WorkflowID == "" {
		fail(c, consts.StatusBadRequest, "workflow_id required")
		return
	}
	wf, err := a.Store.GetWorkflow(ctx, req.WorkflowID)
	if err != nil || wf == nil || wf.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "workflow not found")
		return
	}
	if err := workflowstate.Validate(wf.Steps); err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	r, err := a.Chat.StartWorkflow(context.Background(), *wf, messages.NewID())
	if err != nil {
		code := consts.StatusInternalServerError
		if errors.Is(err, service.ErrExecutionActive) {
			code = consts.StatusConflict
		}
		fail(c, code, "workflow start failed")
		return
	}
	ok(c, map[string]string{"status": r.Status, "conversation_id": r.ConversationID, "run_id": r.RunID})
}

// WorkflowRunStatus returns an owner-scoped parent/step snapshot. Wire as GET
// /workflows/runs/:run_id; it is separate from individual chat run history.
func (a *API) WorkflowRunStatus(ctx context.Context, c *app.RequestContext) {
	r, err := a.Store.GetWorkflowRun(ctx, c.Param("run_id"), authEmail(c))
	if errors.Is(err, db.ErrNotFound) {
		fail(c, consts.StatusNotFound, "workflow run not found")
		return
	}
	if err != nil {
		fail(c, consts.StatusInternalServerError, "workflow status failed")
		return
	}
	ok(c, r)
}
