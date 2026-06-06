package api

import (
	"context"
	"io"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/cavis-oss/cavis_core/messages"
	"github.com/cavis-oss/cavis_control_layer/service"
)

// ChatRun handles POST /chat/run, streaming events as Server-Sent Events.
//
// The response body is an io.Pipe whose reader is handed to Hertz via
// SetBodyStream; a goroutine runs the chat and writes SSE frames into the pipe,
// so events stream to the client incrementally.
func (a *API) ChatRun(ctx context.Context, c *app.RequestContext) {
	var req service.ChatRunRequest
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	if req.UserEmail == "" {
		req.UserEmail = string(c.GetHeader("X-User-Email"))
	}

	pr, pw := io.Pipe()
	c.SetStatusCode(consts.StatusOK)
	c.Response.Header.Set("Content-Type", "text/event-stream")
	c.Response.Header.Set("Cache-Control", "no-cache")
	c.Response.Header.Set("Connection", "keep-alive")
	c.SetBodyStream(pr, -1)

	go func() {
		defer pw.Close()
		// Detach from the Hertz request context (which ends when the handler
		// returns); the run is cancelled via /chat/kill instead.
		a.Chat.Run(context.Background(), req, func(m messages.Message) {
			frame, err := m.SSE()
			if err != nil {
				return
			}
			_, _ = pw.Write(frame) // write error => client gone; run is cancelled elsewhere
		})
	}()
}

type killReq struct {
	TaskID         string `json:"task_id"`
	ConversationID string `json:"conversation_id"`
}

// ChatKill handles POST /chat/kill, cancelling a running session via context.
func (a *API) ChatKill(ctx context.Context, c *app.RequestContext) {
	var req killReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	id := req.TaskID
	if id == "" {
		id = req.ConversationID
	}
	if id == "" {
		fail(c, consts.StatusBadRequest, "task_id or conversation_id required")
		return
	}
	if a.Chat.Kill(id) {
		ok(c, map[string]string{"status": "killed", "id": id})
		return
	}
	fail(c, consts.StatusNotFound, "no running session for id")
}
