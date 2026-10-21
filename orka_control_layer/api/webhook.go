package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/scheduled_task"
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
	raw := c.Request.Body()
	_ = json.Unmarshal(raw, &body)
	event := webhookEvent(string(c.GetHeader("X-GitHub-Event")), body)
	if t.Trigger != nil && t.Trigger.Kind == db.TriggerEvent {
		if t.CronStatus != "on" || (t.Trigger.Event != "" && !strings.EqualFold(event, t.Trigger.Event)) {
			c.JSON(consts.StatusAccepted, map[string]any{"code": 0, "data": map[string]string{"status": "ignored", "event": event}})
			return
		}
	}
	go a.Chat.RunHeadless(context.Background(), webhookRunRequest(t, event, raw, body))
	c.JSON(consts.StatusAccepted, map[string]any{"code": 0, "data": map[string]string{"status": "triggered", "task_id": t.TaskID}})
}

// webhookRunRequest builds the run a webhook call starts. An event task's
// payload goes in as framed untrusted data, and the run carries the task's
// tool policy: whoever can make the calling system emit an event can write
// that payload, so it must not be able to buy more than the task allows.
func webhookRunRequest(t *db.TaskMeta, event string, raw []byte, body map[string]any) service.ChatRunRequest {
	msg := taskPromptFor(t.Variables)
	if m, ok := body["message"].(string); ok && m != "" {
		msg = m
	}
	if t.Trigger != nil && t.Trigger.Kind == db.TriggerEvent {
		msg += "\n\n" + scheduled_task.UntrustedBlock("触发事件 "+firstNonEmptyStr(event, "webhook"), truncRunes(string(raw), 4000))
	}
	return service.ChatRunRequest{
		Message:        msg,
		ConversationID: t.ConversationID,
		TaskID:         t.TaskID,
		UserEmail:      t.OwnerEmail,
		Trigger:        "webhook",
		ToolPolicy:     t.ToolPolicy,
	}
}

func taskPromptFor(vars map[string]any) string {
	for _, k := range []string{"prompt_template", "prompt", "title"} {
		if s, ok := vars[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// webhookEvent names a call: GitHub's event header plus the payload's action
// ("pull_request.closed"), or a generic "event" field.
func webhookEvent(githubEvent string, body map[string]any) string {
	action, _ := body["action"].(string)
	if githubEvent != "" {
		if action != "" {
			return githubEvent + "." + action
		}
		return githubEvent
	}
	event, _ := body["event"].(string)
	return event
}
