package api

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_control_layer/db"
)

// GetPersonal returns what the caller told Orka about themselves.
func (a *API) GetPersonal(ctx context.Context, c *app.RequestContext) {
	p, err := a.Store.GetPersonalization(ctx, authEmail(c))
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		fail(c, consts.StatusInternalServerError, "无法读取个性化设置")
		return
	}
	ok(c, p)
}

// SavePersonal replaces the caller's about-me, reply style and memory switch.
func (a *API) SavePersonal(ctx context.Context, c *app.RequestContext) {
	var req db.Personalization
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request")
		return
	}
	p, err := req.Normalize()
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	if err := a.Store.SavePersonalization(ctx, authEmail(c), p); err != nil {
		fail(c, consts.StatusInternalServerError, "保存失败")
		return
	}
	ok(c, p)
}

// ListMemories returns the caller's saved memories, newest first.
func (a *API) ListMemories(ctx context.Context, c *app.RequestContext) {
	out, err := a.Store.ListMemories(ctx, authEmail(c), db.MaxMemoriesKept)
	if err != nil {
		fail(c, consts.StatusInternalServerError, "无法读取记忆")
		return
	}
	ok(c, out)
}

// ConfirmMemory accepts a memory a run proposed.
func (a *API) ConfirmMemory(ctx context.Context, c *app.RequestContext) {
	var req struct {
		MemoryID string `json:"memory_id"`
	}
	if err := bind(c, &req); err != nil || req.MemoryID == "" {
		fail(c, consts.StatusBadRequest, "memory_id required")
		return
	}
	if err := a.Store.ConfirmMemory(ctx, authEmail(c), req.MemoryID); err != nil {
		fail(c, consts.StatusNotFound, "memory not found")
		return
	}
	ok(c, map[string]string{"memory_id": req.MemoryID})
}

// DeleteMemory permanently removes one of the caller's memories.
func (a *API) DeleteMemory(ctx context.Context, c *app.RequestContext) {
	var req struct {
		MemoryID string `json:"memory_id"`
	}
	if err := bind(c, &req); err != nil || req.MemoryID == "" {
		fail(c, consts.StatusBadRequest, "memory_id required")
		return
	}
	if err := a.Store.DeleteMemory(ctx, authEmail(c), req.MemoryID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			fail(c, consts.StatusNotFound, "memory not found")
			return
		}
		fail(c, consts.StatusInternalServerError, "删除失败")
		return
	}
	ok(c, map[string]string{"memory_id": req.MemoryID})
}
