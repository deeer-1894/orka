package api

import (
	"context"
	"io"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_control_layer/service"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/pathsafe"
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
	// Execution always uses the persisted conversation owner. Unknown IDs and
	// storage failures are rejected rather than treated as new conversations.
	conv, err := a.authorizedConversation(ctx, c, req.ConversationID, true)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	req.UserEmail = conv.OwnerEmail
	if _, err := pathsafe.EnsureSession(a.BaseStorage, req.UserEmail, req.ConversationID); err != nil {
		workspaceFail(c, err)
		return
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
	convID := runID
	if string(c.Query("conversation_id")) == "" && string(c.Query("task_id")) != "" {
		task, err := a.Store.GetTask(ctx, runID)
		if err != nil || task == nil {
			fail(c, 404, "task not found")
			return
		}
		convID = task.ConversationID
	}
	if _, err := a.authorizedConversation(ctx, c, convID, false); err != nil {
		workspaceFail(c, err)
		return
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
	convID := req.ConversationID
	id := convID
	if req.TaskID != "" {
		task, err := a.Store.GetTask(ctx, req.TaskID)
		if err != nil || task == nil || (convID != "" && task.ConversationID != convID) {
			fail(c, 404, "task not found")
			return
		}
		convID = task.ConversationID
		id = req.TaskID
	}
	if _, err := a.authorizedConversation(ctx, c, convID, true); err != nil {
		workspaceFail(c, err)
		return
	}
	if a.Chat.Kill(id) {
		ok(c, map[string]string{"status": "killed", "id": id})
		return
	}
	fail(c, consts.StatusNotFound, "no running session for id")
}

// Followups returns up to 3 suggested next questions for the last Q&A turn.
func (a *API) Followups(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Prompt          string `json:"prompt"`
		SelectedVersion string `json:"selected_version"`
		ModelProfile    string `json:"model_profile"`
		Answer          string `json:"answer"`
	}
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request")
		return
	}
	suggestions, err := a.Chat.SuggestFollowupsForUser(ctx, authEmail(c), req.SelectedVersion, req.ModelProfile, req.Prompt, req.Answer)
	if err != nil {
		settingsError(c, err)
		return
	}
	ok(c, map[string]any{"suggestions": suggestions})
}

// ListModels exposes Auto followed by every explicitly selectable model name.
func (a *API) ListModels(_ context.Context, c *app.RequestContext) {
	if authEmail(c) == "" {
		fail(c, consts.StatusUnauthorized, "authentication required")
		return
	}
	llm, err := a.Chat.ModelConfigForUser(authEmail(c))
	if err != nil {
		settingsError(c, err)
		return
	}
	out := []map[string]string{{"version": service.ModelAuto, "label": "Auto", "hint": "使用列表中的第一个模型"}}
	for _, m := range llm.Models {
		out = append(out, map[string]string{"version": m, "label": m, "hint": "手动指定"})
	}
	ok(c, out)
}

// ToolsCatalog lists the tools available to the caller (with descriptions +
// groups), so the tool picker reflects what the agent can really call instead
// of a hardcoded frontend list.
func (a *API) ToolsCatalog(ctx context.Context, c *app.RequestContext) {
	ok(c, a.Chat.ToolCatalog(ctx, authEmail(c)))
}

// ResumeRun continues a run that died mid-flight, picking up from the surviving
// transcript instead of redoing the work. Detached and published into the
// conversation's stream, exactly like ChatRun, so the client joins it via
// /chat/attach and sees a normal continuation.
func (a *API) ResumeRun(ctx context.Context, c *app.RequestContext) {
	var req struct {
		RunID string `json:"run_id"`
	}
	if err := bind(c, &req); err != nil || req.RunID == "" {
		fail(c, consts.StatusBadRequest, "run_id required")
		return
	}
	email := authEmail(c)
	// Resolve the run before detaching so a bad request gets a real error rather
	// than a stream that opens and immediately dies.
	rec, err := a.Store.GetRun(ctx, req.RunID)
	if err != nil || rec.OwnerEmail != email {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	if !rec.Resumable {
		fail(c, consts.StatusConflict, "这个运行无法继续(没有可恢复的记录)")
		return
	}
	conv := rec.ConversationID
	if _, err := a.authorizedConversation(ctx, c, conv, true); err != nil {
		workspaceFail(c, err)
		return
	}
	a.hub.start(conv)
	go func() {
		if _, err := a.Chat.ResumeRun(context.Background(), req.RunID, email, func(m messages.Message) {
			a.hub.publish(conv, m)
		}); err != nil && a.Log != nil {
			a.Log.Warn("resume run failed", "run_id", req.RunID, "err", err)
		}
		a.hub.finish(conv)
	}()
	ok(c, map[string]any{"resumed": true, "conversation_id": conv, "steps": rec.ResumeSteps})
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
	if _, err := a.authorizedConversation(ctx, c, req.ConversationID, true); err != nil {
		workspaceFail(c, err)
		return
	}
	// Blocking gate (no checkpoint store): the parked tool call is still waiting.
	if a.Chat.ResolveConfirmInConversation(req.ID, req.ConversationID, req.Approve, req.Always) {
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
