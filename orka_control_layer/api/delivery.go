package api

import (
	"context"
	"errors"
	"mime"
	"path/filepath"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/orka-oss/orka_core/delivery"
)

// Immutable deliveries follow current conversation sharing authorization. A
// revoked viewer cannot retain API access by knowing a run ID or a file path.
func (a *API) ListDeliveries(ctx context.Context, c *app.RequestContext) {
	var req struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := bind(c, &req); err != nil {
		fail(c, 400, "invalid request")
		return
	}
	conv, err := a.authorizedConversation(ctx, c, req.ConversationID, false)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	manifests, err := delivery.List(a.BaseStorage, conv.OwnerEmail, req.ConversationID)
	if err != nil {
		fail(c, 500, "delivery archive unavailable")
		return
	}
	ok(c, map[string]any{"deliveries": manifests})
}
func (a *API) DownloadDelivery(ctx context.Context, c *app.RequestContext) {
	convID := string(c.Query("conversation_id"))
	conv, err := a.authorizedConversation(ctx, c, convID, false)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	path := string(c.Query("path"))
	body, err := delivery.Read(a.BaseStorage, conv.OwnerEmail, convID, string(c.Query("run_id")), path)
	if err != nil {
		if errors.Is(err, delivery.ErrIntegrity) {
			fail(c, 409, "delivery integrity check failed")
			return
		}
		fail(c, 404, "delivery file not found")
		return
	}
	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Response.Header.Set("Content-Disposition", contentDisposition("attachment", filepath.Base(path)))
	c.Response.Header.Set("X-Content-Type-Options", "nosniff")
	c.Data(consts.StatusOK, ct, body)
}

// Raw requests and audit history remain owner-only, like /run/get. Sharing a
// deliverable does not disclose internal instructions or unrelated inputs.
func (a *API) GetAcceptance(ctx context.Context, c *app.RequestContext) {
	var req runIDReq
	if err := bind(c, &req); err != nil || req.RunID == "" {
		fail(c, 400, "run_id required")
		return
	}
	if authEmail(c) == "" {
		fail(c, 401, "unauthorized")
		return
	}
	if a.Store == nil || a.Chat == nil {
		fail(c, 503, "run storage unavailable")
		return
	}
	run, err := a.Store.GetRun(ctx, req.RunID)
	if err != nil || run == nil || run.OwnerEmail != authEmail(c) {
		fail(c, 404, "run not found")
		return
	}
	history, err := a.Chat.AcceptanceFor(run.OwnerEmail, run.ConversationID, run.RunID)
	if err != nil {
		fail(c, 500, "acceptance archive unavailable")
		return
	}
	ok(c, history)
}
