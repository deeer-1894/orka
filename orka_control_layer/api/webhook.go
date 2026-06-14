package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_control_layer/service"
)

// Webhook is the PUBLIC trigger endpoint: POST /hook/{token}. The opaque token
// IS the auth (no session needed), so external systems can fire a saved task.
// The JSON body's "message" overrides the prompt; otherwise the task's stored
// prompt runs. Fires detached and returns 202 — observe it in run history.
func (a *API) Webhook(ctx context.Context, c *app.RequestContext) {
	token := c.Param("token")
	if token == "" {
		fail(c, consts.StatusBadRequest, "missing token")
		return
	}
	t, err := a.Store.GetTaskByWebhook(ctx, token)
	if err != nil {
		fail(c, consts.StatusNotFound, "no task for this webhook")
		return
	}
	var body map[string]any
	_ = json.Unmarshal(c.Request.Body(), &body)
	msg := taskPromptFor(t.Variables)
	if m, ok := body["message"].(string); ok && m != "" {
		msg = m
	}
	go a.Chat.RunHeadless(context.Background(), service.ChatRunRequest{
		Message:        msg,
		ConversationID: t.ConversationID,
		TaskID:         t.TaskID,
		UserEmail:      t.OwnerEmail,
		Trigger:        "webhook",
	})
	c.JSON(consts.StatusAccepted, map[string]any{"code": 0, "data": map[string]string{"status": "triggered", "task_id": t.TaskID}})
}

func taskPromptFor(vars map[string]any) string {
	for _, k := range []string{"prompt_template", "prompt", "title"} {
		if s, ok := vars[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// EnableWebhook generates a webhook token for a task and returns its trigger URL.
func (a *API) EnableWebhook(ctx context.Context, c *app.RequestContext) {
	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := bind(c, &req); err != nil || req.TaskID == "" {
		fail(c, consts.StatusBadRequest, "task_id required")
		return
	}
	t, err := a.Store.GetTask(ctx, req.TaskID)
	if err != nil || t.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "task not found")
		return
	}
	var b [18]byte
	_, _ = rand.Read(b[:])
	token := hex.EncodeToString(b[:])
	if err := a.Store.SetTaskWebhook(ctx, req.TaskID, token); err != nil {
		fail(c, consts.StatusInternalServerError, "enable failed")
		return
	}
	ok(c, map[string]string{"token": token, "path": "/api/v1/controller/hook/" + token})
}

// DisableWebhook clears a task's webhook token.
func (a *API) DisableWebhook(ctx context.Context, c *app.RequestContext) {
	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := bind(c, &req); err != nil || req.TaskID == "" {
		fail(c, consts.StatusBadRequest, "task_id required")
		return
	}
	t, err := a.Store.GetTask(ctx, req.TaskID)
	if err != nil || t.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "task not found")
		return
	}
	_ = a.Store.SetTaskWebhook(ctx, req.TaskID, "")
	ok(c, map[string]string{"status": "disabled"})
}
