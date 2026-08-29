package api

import (
	"context"
	"io"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/service"
)

// ChatRun handles POST /chat/run, streaming events as Server-Sent Events.
//
// Events are published to a per-conversation streamHub (which assigns sequence
// ids and buffers recent frames), and this response subscribes from seq 0. The
// run itself is detached from the request context, so if the client disconnects
// it keeps producing events; the client can reconnect via /chat/attach and
// replay everything it missed (Last-Event-ID).
func (a *API) ChatRun(ctx context.Context, c *app.RequestContext) {
	var req service.ChatRunRequest
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	// authenticated identity always wins over any client-supplied email
	me := authEmail(c)
	if me != "" {
		req.UserEmail = me
	}
	// Sharing authz: for an existing conversation owned by someone else, only an
	// editor may send (viewers are read-only), and the run executes in the
	// OWNER's context (files/memory/workspace) so the shared thread stays
	// coherent. A brand-new conversation has no record yet → the sender owns it.
	if req.ConversationID != "" {
		if conv, err := a.Store.GetConversation(ctx, req.ConversationID); err == nil && conv.OwnerEmail != "" && conv.OwnerEmail != me {
			if !conv.CanWrite(me) {
				fail(c, consts.StatusForbidden, "只读访问:你没有此会话的编辑权限")
				return
			}
			req.UserEmail = conv.OwnerEmail
		}
	}

	runID := firstNonEmptyStr(req.ConversationID, req.TaskID)
	rs := a.hub.start(runID)

	go func() {
		// Detach from the Hertz request context (which ends when the handler
		// returns); the run is cancelled via /chat/kill instead.
		a.Chat.Run(context.Background(), req, func(m messages.Message) {
			a.hub.publish(runID, m)
		})
		a.hub.finish(runID)
	}()

	a.streamRun(c, rs, 0)
}

// ChatAttach handles GET /chat/attach?conversation_id=..&last_event_id=N — it
// re-attaches an SSE client to an in-progress run and replays missed events.
func (a *API) ChatAttach(ctx context.Context, c *app.RequestContext) {
	runID := string(c.Query("conversation_id"))
	if runID == "" {
		runID = string(c.Query("task_id"))
	}
	rs := a.hub.get(runID)
	if rs == nil {
		fail(c, consts.StatusNotFound, "no active run for id")
		return
	}
	var from int64
	if v := string(c.Query("last_event_id")); v != "" {
		from, _ = strconv.ParseInt(v, 10, 64)
	}
	a.streamRun(c, rs, from)
}

// streamRun writes SSE frames (with id: lines for reconnect) from a runStream,
// starting after fromSeq, until the run finishes or the client disconnects.
func (a *API) streamRun(c *app.RequestContext, rs *runStream, fromSeq int64) {
	ch, replay, done, cancel := rs.subscribe(fromSeq)

	pr, pw := io.Pipe()
	c.SetStatusCode(consts.StatusOK)
	c.Response.Header.Set("Content-Type", "text/event-stream")
	c.Response.Header.Set("Cache-Control", "no-cache")
	c.Response.Header.Set("Connection", "keep-alive")
	c.SetBodyStream(pr, -1)

	go func() {
		defer pw.Close()
		defer cancel()
		write := func(sf seqFrame) bool {
			if _, err := pw.Write([]byte("id: " + strconv.FormatInt(sf.seq, 10) + "\n")); err != nil {
				return false
			}
			_, err := pw.Write(sf.data)
			return err == nil
		}
		for _, sf := range replay {
			if !write(sf) {
				return
			}
		}
		if done {
			return // run already finished; replay was all that's left
		}
		for sf := range ch {
			if !write(sf) {
				return // client gone; run continues in the background
			}
		}
	}()
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
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

// ListModels handles GET /models, exposing the configured model names so the UI
// shows real labels (deepseek / vLLM / OpenAI…) instead of a hardcoded string.
// Only the two user-facing tiers are surfaced (main / mini) — base_url and other
// ops details are intentionally not exposed to the client.
// Followups returns up to 3 suggested next questions for the last Q&A turn.
func (a *API) Followups(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Prompt string `json:"prompt"`
		Answer string `json:"answer"`
	}
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request")
		return
	}
	ok(c, map[string]any{"suggestions": a.Chat.SuggestFollowups(ctx, req.Prompt, req.Answer)})
}

func (a *API) ListModels(_ context.Context, c *app.RequestContext) {
	llm := a.Chat.Cfg.LLM
	out := []map[string]string{{"version": "", "label": llm.Model, "hint": "主模型 · 更强"}}
	if llm.MiniModel != "" && llm.MiniModel != llm.Model {
		out = append(out, map[string]string{"version": "mini", "label": llm.MiniModel, "hint": "更快 · 更省"})
	}
	ok(c, out)
}

// ToolsCatalog lists the tools available to the caller (with descriptions +
// groups), so the tool picker reflects what the agent can really call instead
// of a hardcoded frontend list.
func (a *API) ToolsCatalog(ctx context.Context, c *app.RequestContext) {
	ok(c, a.Chat.ToolCatalog(ctx, authEmail(c)))
}

// ConfirmAction approves or rejects a paused side-effecting tool call.
func (a *API) ConfirmAction(ctx context.Context, c *app.RequestContext) {
	var req struct {
		ID             string `json:"id"`
		ConversationID string `json:"conversation_id"`
		Approve        bool   `json:"approve"`
		Always         bool   `json:"always"` // approve for the rest of this conversation
	}
	if err := bind(c, &req); err != nil || req.ID == "" {
		fail(c, consts.StatusBadRequest, "id required")
		return
	}
	// Blocking gate (no checkpoint store): the parked tool call is still waiting.
	if a.Chat.ResolveConfirm(req.ID, req.Approve, req.Always) {
		ok(c, map[string]bool{"resolved": true})
		return
	}

	// Interrupt/resume gate: nothing is parked — the run was checkpointed and must
	// be resumed. Detached like ChatRun, republishing into the same SSE stream so
	// a connected client sees the continuation.
	// The interrupt address identifies the tool call, not the conversation, so
	// the client sends conversation_id alongside it.
	conv := req.ConversationID
	if conv == "" {
		fail(c, consts.StatusBadRequest, "conversation_id required to resume a paused run")
		return
	}
	if _, _, _, pending := a.Chat.PendingConfirm(conv); !pending {
		fail(c, consts.StatusNotFound, "no pending confirmation (expired?)")
		return
	}
	a.hub.start(conv)
	go func() {
		a.Chat.ResumeConfirm(context.Background(), conv, req.Approve, req.Always, func(m messages.Message) {
			a.hub.publish(conv, m)
		})
		a.hub.finish(conv)
	}()
	ok(c, map[string]bool{"resolved": true, "resumed": true})
}
