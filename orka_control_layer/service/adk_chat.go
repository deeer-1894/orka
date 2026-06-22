package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_core/agent"
	cp "github.com/orka-oss/orka_core/checkpoint"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/trace"

	"github.com/orka-oss/orka_control_layer/checkpoint"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/message_utils"
	"github.com/orka-oss/orka_control_layer/obs"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
)

// ChatRunRequest is the /chat/run payload.
type ChatRunRequest struct {
	Message         string   `json:"message"`
	ConversationID  string   `json:"conversation_id"`
	TaskID          string   `json:"task_id"`
	EnabledTools    []string `json:"enabled_tools"`
	FileIDs         []string `json:"file_ids"`
	TemplateID      string   `json:"template_id"`
	SelectedVersion string   `json:"selected_version"`
	ResumeKey       string   `json:"resume_key"`
	UserEmail       string   `json:"user_email"`
	ActiveSkill     string   `json:"active_skill"` // user-locked skill mode (deterministic prompt injection)
	Trigger         string   `json:"trigger"`      // manual | schedule (audit: how the run was started)
	ConfirmRisky    bool     `json:"confirm_risky"` // gate side-effecting tools behind user approval
}

// ToolsProvider supplies the tool set for a request and an optional cleanup
// (e.g. closing a per-request MCP client). cleanup may be nil.
type ToolsProvider func(ctx context.Context, req ChatRunRequest) (tools []agent.BaseTool, cleanup func(), err error)

// ChatService runs the end-to-end chat path.
type ChatService struct {
	Cfg     *config.Config
	Main    llm.Client
	Mini    llm.Client
	CP      checkpoint.Store
	Msg     *message_utils.Messenger
	Metrics *obs.Metrics
	Log     *slog.Logger
	ToolsFor ToolsProvider
	// InvalidateTools busts a user's cached tool connections (e.g. after they
	// add/remove an MCP connector). Set by main when the pooled provider is wired.
	InvalidateTools func(email string)
	// DisableSummary turns off the eino summarization middleware. Production keeps
	// it on (folds long context into a running summary); deterministic tests set
	// it so a scripted mock isn't consumed by the summarizer's extra model call.
	DisableSummary bool

	confirms    *confirmHub // pending approval gates for risky tools
	confirmInit sync.Once

	mu   sync.Mutex
	runs map[string]context.CancelFunc
}

// NewChatService builds a ChatService with sane defaults. By default it serves
// the real local filesystem tools (per-user root) plus the GUI mock; callers may
// override ToolsFor to add MCP tools from tools_server.
func NewChatService(cfg *config.Config, main, mini llm.Client, store checkpoint.Store, msg *message_utils.Messenger, metrics *obs.Metrics, log *slog.Logger) *ChatService {
	s := &ChatService{
		Cfg: cfg, Main: main, Mini: mini, CP: store, Msg: msg, Metrics: metrics, Log: log,
		runs: map[string]context.CancelFunc{},
	}
	s.ToolsFor = LocalToolsProvider(cfg.Storage.BaseStoragePath)
	return s
}

func (s *ChatService) modelFor(version string) (llm.Client, string) {
	if version == "mini" && s.Mini != nil {
		return s.Mini, s.Cfg.LLM.MiniModel
	}
	return s.Main, s.Cfg.LLM.Model
}

func (s *ChatService) register(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	s.runs[id] = cancel
	s.mu.Unlock()
}

func (s *ChatService) unregister(id string) {
	s.mu.Lock()
	delete(s.runs, id)
	s.mu.Unlock()
}

// Kill cancels a running session by id (task_id or conversation_id). Returns
// true if a session was found. Uses channel/context cancellation, not polling.
func (s *ChatService) Kill(id string) bool {
	s.mu.Lock()
	cancel, ok := s.runs[id]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Run executes one chat request, streaming events through raw. It blocks until
// the run completes, interrupts (clarify) or is cancelled. raw writes SSE frames.
func (s *ChatService) Run(parent context.Context, req ChatRunRequest, raw func(messages.Message)) string {
	traceID := trace.NewTraceID()
	meta := messages.Meta{
		ConversationID: req.ConversationID,
		TaskID:         req.TaskID,
		TraceID:        traceID,
		UserEmail:      req.UserEmail,
		ModelVersion:   req.SelectedVersion,
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	runID := firstNonEmpty(req.TaskID, req.ConversationID, traceID)
	s.register(runID, cancel)
	defer s.unregister(runID)

	if s.Metrics != nil {
		s.Metrics.ActiveSessions.Add(1)
		defer s.Metrics.ActiveSessions.Add(-1)
	}

	// heartbeat keeps the SSE connection alive during long runs.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go s.heartbeat(hbCtx, meta, raw)

	model, modelName := s.modelFor(req.SelectedVersion)

	// Root trace span for the whole run; tool spans (in tools-mid) nest under it.
	spanCtx, endSpan := trace.StartSpan(trace.WithTraceID(ctx, traceID), "chat.run", map[string]string{
		"conversation_id": req.ConversationID,
		"model":           modelName,
		"runtime":         "eino",
		"resume":          boolStr(req.ResumeKey != ""),
	})
	defer endSpan()
	ctx = spanCtx
	// Local tools (e.g. artifact_publish) learn whose run they're in from ctx.
	ctx = WithRunInfo(ctx, req.ConversationID, req.UserEmail)

	deps := PipelineDeps{LLM: model, Model: modelName, Metrics: s.Metrics}

	tools, cleanup, err := s.ToolsFor(ctx, req)
	if err != nil && s.Log != nil {
		s.Log.Warn("tools provider degraded", "trace_id", traceID, "err", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	// Gate side-effecting tools behind a user approval when requested (only for
	// interactive runs — a scheduled/headless run has no one to approve).
	if req.ConfirmRisky && req.Trigger != "schedule" && req.Trigger != "workflow" {
		tools = s.wrapConfirm(tools)
	}

	// Multi-agent: the orchestrator (main model) gets the atomic tools PLUS native
	// eino sub-agents (researcher/writer/browser/engineer, mini model) it can
	// delegate to. The sub-agents are built inside runEino from the atomic tool set.
	if s.Cfg.Agent.MultiAgent {
		deps.SystemPrompt = OrchestratorPrompt
	}

	// A user-locked skill deterministically injects its expert guidance into the
	// system prompt (vs. waiting for the model to adopt it via apply_skill).
	if sp, ok := middlewares.SkillPrompt(req.ActiveSkill); ok {
		base := deps.SystemPrompt
		if base == "" {
			base = middlewares.DefaultSystemPrompt
		}
		deps.SystemPrompt = base + "\n\n[Active skill mode]\n" + sp
	}

	rc := &agent.RunContext{Ctx: ctx, Vars: map[string]any{}, Meta: meta, Tools: tools}
	// Serialize emits PER RUN (not globally): parallel sub-agents stream events
	// concurrently into the same SSE sink, so without this their frames could
	// interleave/corrupt. A per-run mutex keeps each run's stream ordered without
	// coupling independent conversations.
	var emitMu sync.Mutex
	rc.Send = func(m messages.Message) {
		emitMu.Lock()
		defer emitMu.Unlock()
		s.Msg.Deliver(rc, raw, m, true)
	}
	// Expose the emit sink + run meta on the context so tools — including
	// sub-agents (AgentTool) — can stream events into the same SSE stream and
	// inherit conversation/trace identity.
	rc.Ctx = agent.WithEmit(agent.WithMeta(ctx, meta), rc.Send)

	// Record this execution as an auditable run (the automation platform's unit).
	startedAt := time.Now().UnixMilli()
	trigger := req.Trigger
	if trigger == "" {
		trigger = "manual"
	}
	if req.ResumeKey != "" {
		trigger = "resume"
	}
	runRecID := s.createRun(ctx, req, meta, trigger)

	if req.ResumeKey != "" {
		err = s.resume(ctx, rc, req, raw, deps, tools, model, modelName)
		if err != nil {
			return s.finalizeRun(runRecID, rc, startedAt, req, err, ctx.Err()) // resume() already emitted the failure event
		}
	} else {
		// Seed with prior turns (memory) + persist the new user message
		// (raw=nil: persist only; the SSE echo is rendered optimistically).
		history := s.loadChatHistory(ctx, req.ConversationID, meta)
		// First turn → title the conversation. Set a snippet immediately (so the
		// sidebar updates right away) then refine it with a mini LLM summary
		// asynchronously (does not block the run, survives SSE disconnect).
		if len(history) == 0 && req.ConversationID != "" && s.Msg != nil && s.Msg.Store != nil {
			_ = s.Msg.Store.UpdateConversationTitle(ctx, req.ConversationID, titleSnippet(req.Message))
			s.titleAsync(req.ConversationID, req.Message)
		}
		userMsg := messages.Chat(messages.RoleUser, req.Message, meta)
		// Fold any uploaded attachments (text inline; images via a VLM pre-pass)
		// into the message the model sees — the optimistic UI echo keeps the
		// user's original text, so this context is invisible in the bubble.
		modelMsg := userMsg
		if extra := s.processAttachments(ctx, req); extra != "" {
			modelMsg.Content += extra
		}
		rc.Messages = append(history, modelMsg)
		s.Msg.Deliver(rc, nil, userMsg, true)
		s.Msg.Deliver(rc, raw, messages.Task("start", meta), true)
		err = s.runEino(ctx, rc, deps, tools, model, modelName, raw)
	}

	s.finish(ctx, rc, meta, raw, err)
	return s.finalizeRun(runRecID, rc, startedAt, req, err, ctx.Err())
}

// RunHeadless runs a detached (scheduled/webhook) request with task-level
// auto-retry: if the run fails and the task declares RetryCount > 0, it re-runs
// up to that many times with linear backoff. Each attempt is its own run record.
func (s *ChatService) RunHeadless(ctx context.Context, req ChatRunRequest) {
	retries := 0
	if req.TaskID != "" && s.Msg != nil && s.Msg.Store != nil {
		if t, err := s.Msg.Store.GetTask(ctx, req.TaskID); err == nil {
			retries = t.RetryCount
		}
	}
	for attempt := 0; ; attempt++ {
		status := s.Run(ctx, req, func(messages.Message) {})
		if status != db.RunFailed || attempt >= retries || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(attempt+1) * 5 * time.Second): // linear backoff
		}
		if s.Log != nil {
			s.Log.Info("task retry", "task_id", req.TaskID, "attempt", attempt+1, "of", retries)
		}
	}
}

// createRun opens a RunRecord (status=running) for this execution.
func (s *ChatService) createRun(ctx context.Context, req ChatRunRequest, meta messages.Meta, trigger string) string {
	if s.Msg == nil || s.Msg.Store == nil || req.UserEmail == "" {
		return ""
	}
	id := "run_" + messages.NewID()
	r := &db.RunRecord{
		RunID:          id,
		TaskID:         req.TaskID,
		ConversationID: req.ConversationID,
		OwnerEmail:     req.UserEmail,
		Trigger:        trigger,
		Status:         db.RunRunning,
		Prompt:         req.Message,
		TraceID:        meta.TraceID,
		CreatedAt:      time.Now().UnixMilli(),
	}
	if s.Msg.Store.CreateRun(ctx, r) != nil {
		return ""
	}
	return id
}

// finalizeRun stamps a run's terminal state + execution stats. Uses a background
// context since the request context may already be cancelled.
func (s *ChatService) finalizeRun(runID string, rc *agent.RunContext, startedAt int64, req ChatRunRequest, runErr, ctxErr error) string {
	status, errStr := db.RunDone, ""
	switch {
	case runErr == context.Canceled || ctxErr == context.Canceled:
		status, errStr = db.RunFailed, "cancelled"
	case runErr != nil:
		status, errStr = db.RunFailed, runErr.Error()
	case rc.Interrupt != nil:
		status = db.RunPaused
	}
	if runID == "" || s.Msg == nil || s.Msg.Store == nil {
		return status
	}
	now := time.Now().UnixMilli()
	out := middlewares.Final(rc)
	tokens := middlewares.RunTokens(rc)
	toolCalls := middlewares.RunTools(rc)
	bg := context.Background()
	_ = s.Msg.Store.FinalizeRun(bg, db.RunRecord{
		RunID: runID, Status: status, Error: errStr,
		Output: trunc(out, 400), Result: extractJSON(out), Tokens: tokens, ToolCalls: toolCalls,
		FinishedAt: now, DurationMs: now - startedAt,
	})
	// Alert on UNATTENDED failures (scheduled/webhook/rerun) — a manual failure
	// the user is already watching on screen.
	if status == db.RunFailed && req.Trigger != "" && req.Trigger != "manual" && req.UserEmail != "" {
		_ = s.Msg.Store.CreateNotification(bg, &db.Notification{
			NotificationID: "ntf_" + messages.NewID(),
			OwnerEmail:     req.UserEmail,
			Kind:           "run_failed",
			Title:          "自动任务运行失败",
			Body:           trunc(req.Message, 80) + " — " + trunc(errStr, 120),
			RunID:          runID,
			ConversationID: req.ConversationID,
			CreatedAt:      now,
		})
	}
	return status
}

// extractJSON pulls a structured result out of an answer so runs are
// programmatically consumable (chaining / external systems): prefers a ```json
// fenced block, else a leading top-level {…} / […]. Returns "" when valid JSON
// isn't present (most answers are prose). Capped to avoid bloating the record.
func extractJSON(text string) string {
	candidate := ""
	if i := strings.Index(text, "```json"); i >= 0 {
		rest := text[i+len("```json"):]
		if j := strings.Index(rest, "```"); j >= 0 {
			candidate = strings.TrimSpace(rest[:j])
		}
	}
	if candidate == "" {
		t := strings.TrimSpace(text)
		if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
			candidate = t
		}
	}
	if candidate == "" {
		return ""
	}
	var probe any
	if json.Unmarshal([]byte(candidate), &probe) != nil {
		return "" // not valid JSON
	}
	if len(candidate) > 8000 {
		return ""
	}
	return candidate
}

// trunc caps a string to n runes for storage/display.
func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// loadChatHistory returns the prior user/assistant turns of a conversation in
// chronological order, giving the LLM multi-turn memory.
func (s *ChatService) loadChatHistory(ctx context.Context, convID string, meta messages.Meta) []messages.Message {
	if s.Msg == nil || s.Msg.Store == nil || convID == "" {
		return nil
	}
	rows, err := s.Msg.Store.GetMessages(ctx, convID, 0, 60)
	if err != nil {
		return nil
	}
	out := make([]messages.Message, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- { // GetMessages is newest-first; reverse
		r := rows[i]
		if r.Type != string(messages.EventChat) {
			continue
		}
		if r.Role != messages.RoleUser && r.Role != messages.RoleAssistant {
			continue
		}
		out = append(out, messages.Chat(r.Role, r.Content, meta))
	}
	return out
}

// resume claims the checkpoint (idempotent) and continues the run.
func (s *ChatService) resume(ctx context.Context, rc *agent.RunContext, req ChatRunRequest, raw func(messages.Message), deps PipelineDeps, tools []agent.BaseTool, model llm.Client, modelName string) error {
	c, err := s.CP.Claim(ctx, req.ResumeKey)
	if err != nil {
		// not found / already consumed -> reject duplicate or expired resume
		s.Msg.Deliver(rc, raw, taskFailed(rc.Meta, "resume rejected: checkpoint not found or already used"), true)
		return err
	}
	if s.Metrics != nil {
		s.Metrics.Checkpoints.Add(-1)
	}
	rc.Messages = c.Messages
	rc.Cursor = c.Cursor
	if c.Vars != nil {
		rc.Vars = c.Vars
	}
	s.Msg.Deliver(rc, raw, messages.Task("running", rc.Meta), true)
	// Stateless resume: fold the user's answer into history and re-run the eino
	// agent over the augmented messages (the clarify question was already recorded
	// into the checkpoint's history before the pause).
	if req.Message != "" {
		um := messages.Chat(messages.RoleUser, req.Message, rc.Meta)
		rc.Messages = append(rc.Messages, um)
		s.Msg.Deliver(rc, nil, um, true)
	}
	rc.Interrupt = nil
	return s.runEino(ctx, rc, deps, tools, model, modelName, raw)
}

// finish handles the terminal state: error, clarify interrupt, or done.
func (s *ChatService) finish(ctx context.Context, rc *agent.RunContext, meta messages.Meta, raw func(messages.Message), err error) {
	switch {
	case err == context.Canceled || ctx.Err() == context.Canceled:
		s.Msg.Deliver(rc, raw, taskFailed(meta, "cancelled"), true)
	case err != nil:
		if s.Log != nil {
			s.Log.Error("chat run failed", "trace_id", meta.TraceID, "err", err)
		}
		// Surface a friendly assistant message (not the raw upstream error).
		s.Msg.Deliver(rc, raw, messages.Chat(messages.RoleAssistant, friendlyErr(err), meta), true)
		s.Msg.Deliver(rc, raw, taskFailed(meta, err.Error()), true)
	case rc.Interrupt != nil && rc.Interrupt.Clarify != nil:
		s.persistClarify(ctx, rc, meta, raw)
	default:
		s.Msg.Deliver(rc, raw, messages.Task("done", meta), true)
	}
}

// persistClarify saves the checkpoint and emits the clarify question.
func (s *ChatService) persistClarify(ctx context.Context, rc *agent.RunContext, meta messages.Meta, raw func(messages.Message)) {
	key := "cp_" + messages.NewID()
	c := &cp.Checkpoint{
		Messages:  rc.Messages,
		Cursor:    rc.Cursor,
		Vars:      rc.Vars,
		Meta:      meta,
		Version:   1,
		CreatedAt: time.Now().UnixMilli(),
		TTLSec:    s.Cfg.Agent.CheckpointTTLSec,
	}
	if err := s.CP.Save(ctx, key, c); err != nil {
		if s.Log != nil {
			s.Log.Error("save checkpoint", "trace_id", meta.TraceID, "err", err)
		}
		s.Msg.Deliver(rc, raw, taskFailed(meta, "failed to persist clarify checkpoint"), true)
		return
	}
	if s.Metrics != nil {
		s.Metrics.Checkpoints.Add(1)
	}
	clar := *rc.Interrupt.Clarify
	clar.ResumeKey = key
	s.Msg.Deliver(rc, raw, messages.Clarify(clar, meta), true)
	s.Msg.Deliver(rc, raw, messages.Task("paused", meta), true)
}

func (s *ChatService) heartbeat(ctx context.Context, meta messages.Meta, raw func(messages.Message)) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			raw(messages.Heartbeat(meta))
		}
	}
}

// friendlyErr maps low-level run errors to a user-readable message. It branches
// on the typed llm.APIError (HTTP status) and context errors rather than on
// brittle error-string matching; the status body is only inspected for the
// provider-specific content-moderation signal.
func friendlyErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "处理超时了,请重试或简化任务。"
	}
	var apiErr *llm.APIError
	if errors.As(err, &apiErr) {
		body := strings.ToLower(apiErr.Body)
		if strings.Contains(body, "content exists risk") || (strings.Contains(body, "content") && strings.Contains(body, "risk")) {
			return "抱歉,这条请求被模型的内容安全策略拦截了,请换个说法再试。"
		}
		switch {
		case apiErr.Status == 401 || apiErr.Status == 403:
			return "模型服务鉴权失败,请检查 API key 配置。"
		case apiErr.Status == 429:
			return "模型服务请求过于频繁(限流),请稍后再试。"
		case apiErr.Status/100 == 4:
			return "请求被模型服务拒绝(参数或内容问题),请调整后重试。"
		default:
			return "模型服务暂时不可用,请稍后再试。"
		}
	}
	e := err.Error()
	if strings.Contains(e, "context deadline") || strings.Contains(e, "timeout") {
		return "处理超时了,请重试或简化任务。"
	}
	return "抱歉,处理时出错了:" + e
}

func taskFailed(meta messages.Meta, reason string) messages.Message {
	m := messages.Task("failed", meta)
	m.Content = reason
	return m
}

// titleAsync refines the conversation title using a mini LLM summary of the
// first message. It runs in the background with its own short-lived context so
// it neither blocks the chat run nor dies when the SSE connection closes.
func (s *ChatService) titleAsync(convID, message string) {
	model, modelName := s.modelFor("mini")
	if model == nil || s.Msg == nil || s.Msg.Store == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		resp, err := model.Chat(ctx, llm.Request{Model: modelName, Messages: []llm.ChatMessage{
			{Role: llm.RoleSystem, Content: "You generate a very short chat title (max 6 words) summarizing the user's first message. Reply with ONLY the title — same language as the message, no quotes, no punctuation at the end, no prefixes."},
			{Role: llm.RoleUser, Content: message},
		}})
		if err != nil {
			return // keep the snippet title
		}
		title := cleanTitle(resp.Content)
		if title == "" {
			return
		}
		_ = s.Msg.Store.UpdateConversationTitle(ctx, convID, title)
	}()
}

// cleanTitle trims the model's title output to a safe single-line label.
func cleanTitle(s string) string {
	t := strings.TrimSpace(s)
	t = strings.Trim(t, "\"'“”「」 \t\n")
	if i := strings.IndexAny(t, "\n\r"); i >= 0 {
		t = t[:i]
	}
	r := []rune(t)
	if len(r) > 30 {
		t = string(r[:30]) + "…"
	}
	return strings.TrimSpace(t)
}

// titleSnippet derives a short conversation title from the first message.
func titleSnippet(msg string) string {
	t := strings.TrimSpace(strings.ReplaceAll(msg, "\n", " "))
	r := []rune(t)
	if len(r) > 24 {
		return string(r[:24]) + "…"
	}
	if len(r) == 0 {
		return "New chat"
	}
	return t
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
