package api

import (
	"context"
	"errors"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/messages"
)

type projectReq struct {
	ProjectID    string `json:"project_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
	DefaultSkill string `json:"default_skill"`
}

// ListProjects returns the caller's projects, most recently changed first.
func (a *API) ListProjects(ctx context.Context, c *app.RequestContext) {
	out, err := a.Store.ListProjects(ctx, authEmail(c))
	if err != nil {
		fail(c, consts.StatusInternalServerError, "无法读取项目")
		return
	}
	ok(c, out)
}

// SaveProject creates a project (no project_id) or updates the caller's own.
func (a *API) SaveProject(ctx context.Context, c *app.RequestContext) {
	var req projectReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request")
		return
	}
	email, now := authEmail(c), time.Now().UnixMilli()
	p, err := db.Project{ProjectID: req.ProjectID, OwnerEmail: email, Name: req.Name, Description: req.Description,
		Instructions: req.Instructions, DefaultSkill: req.DefaultSkill, UpdatedAt: now}.Normalize()
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	if p.ProjectID == "" {
		p.ProjectID, p.CreatedAt = "prj_"+messages.NewID(), now
		if err := a.Store.CreateProject(ctx, &p); err != nil {
			fail(c, consts.StatusInternalServerError, "创建失败")
			return
		}
		ok(c, p)
		return
	}
	existing, err := a.Store.GetProject(ctx, email, p.ProjectID)
	if err != nil {
		fail(c, consts.StatusNotFound, "project not found")
		return
	}
	p.CreatedAt = existing.CreatedAt
	if err := a.Store.UpdateProject(ctx, &p); err != nil {
		fail(c, consts.StatusInternalServerError, "保存失败")
		return
	}
	ok(c, p)
}

// DeleteProject removes the caller's project; its conversations return to the
// general list and every file stays where it is.
func (a *API) DeleteProject(ctx context.Context, c *app.RequestContext) {
	var req projectReq
	if err := bind(c, &req); err != nil || req.ProjectID == "" {
		fail(c, consts.StatusBadRequest, "project_id required")
		return
	}
	if err := a.Store.DeleteProject(ctx, authEmail(c), req.ProjectID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			fail(c, consts.StatusNotFound, "project not found")
			return
		}
		fail(c, consts.StatusInternalServerError, "删除失败")
		return
	}
	ok(c, map[string]string{"project_id": req.ProjectID})
}

// MoveConversation files the caller's own conversation under one of the
// caller's projects, or takes it out (project_id "").
func (a *API) MoveConversation(ctx context.Context, c *app.RequestContext) {
	var req struct {
		ConversationID string `json:"conversation_id"`
		ProjectID      string `json:"project_id"`
	}
	if err := bind(c, &req); err != nil || req.ConversationID == "" {
		fail(c, consts.StatusBadRequest, "conversation_id required")
		return
	}
	email := authEmail(c)
	if req.ProjectID != "" {
		if _, err := a.Store.GetProject(ctx, email, req.ProjectID); err != nil {
			fail(c, consts.StatusNotFound, "project not found")
			return
		}
	}
	if err := a.Store.MoveConversation(ctx, email, req.ConversationID, req.ProjectID); err != nil {
		fail(c, consts.StatusNotFound, "conversation not found")
		return
	}
	ok(c, map[string]string{"conversation_id": req.ConversationID, "project_id": req.ProjectID})
}
