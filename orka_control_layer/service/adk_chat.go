package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cloudwego/eino/adk"
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
	ActiveSkill     string   `json:"active_skill"`  // user-locked skill mode (deterministic prompt injection)
	Trigger         string   `json:"trigger"`       // manual | schedule (audit: how the run was started)
	ConfirmRisky    bool     `json:"confirm_risky"` // gate side-effecting tools behind user approval

	// Internal (never bound from JSON): set when resuming an interrupted run.
	resumeTarget string     // InterruptCtx.ID of the paused tool call
	resumeData   any        // the user's decision, handed to that tool
	resumeFrom   *runResume // recovered transcript of a run that died mid-flight
}

// ToolsProvider supplies the tool set for a request and an optional cleanup
// (e.g. closing a per-request MCP client). cleanup may be nil.
type ToolsProvider func(ctx context.Context, req ChatRunRequest) (tools []agent.BaseTool, cleanup func(), err error)

// ChatService runs the end-to-end chat path.
type ChatService struct {
	Cfg      *config.Config
	Main     llm.Client
	Mini     llm.Client
	CP       checkpoint.Store
	Msg      *message_utils.Messenger
	Metrics  *obs.Metrics
	Log      *slog.Logger
	ToolsFor ToolsProvider
	// InvalidateTools busts a user's cached tool connections (e.g. after they
	// add/remove an MCP connector). Set by main when the pooled provider is wired.
	InvalidateTools func(email string)
	// OnEvent, when set, pushes a per-user UI-invalidation signal (e.g. "run",
	// "notification") to the event bus so a user's open tabs refresh immediately
	// after background work finishes. Wired by main to the API event hub.
	OnEvent func(email, kind string)
	// DisableSummary turns off the eino summarization middleware. Production keeps
	// it on (folds long context into a running summary); deterministic tests set
	// it so a scripted mock isn't consumed by the summarizer's extra model call.
	DisableSummary bool

	confirms    *confirmHub // pending approval gates for risky tools
	confirmInit sync.Once

	ckpt     adk.CheckPointStore // interrupt/resume checkpoints (nil = blocking gate)
	ckptInit sync.Once

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

// modelFor resolves a request's selected_version to a client and model name.
//
// "" is the main tier and "mini" the fast one, as before. Anything else is
// treated as an explicit model NAME: providers that host many models behind one
// endpoint serve all of them from the same client, so picking one is a matter
// of the name alone. Unknown names fall back to the main tier rather than being
// forwarded — a request must not be able to bill an arbitrary model.
//
// ModelAuto is resolved by the router, not here; it starts on the fast tier and
// this returns that, so a caller with no router still behaves sensibly.
func (s *ChatService) modelFor(version string) (llm.Client, string) {
	switch version {
	case "":
		return s.Main, s.Cfg.LLM.Model
	case "mini", ModelAuto:
		if s.Mini != nil {
			return s.Mini, s.Cfg.LLM.MiniModel
		}
		return s.Main, s.Cfg.LLM.Model
	}
	if s.Cfg.LLM.AllowsModel(version) {
		return s.Main, version
	}
	return s.Main, s.Cfg.LLM.Model
}

// strongModelFor is the tier the router escalates to for a given selection.
func (s *ChatService) strongModelFor(version string) (llm.Client, string) {
	if version != "" && version != "mini" && version != ModelAuto && s.Cfg.LLM.AllowsModel(version) {
		return s.Main, version // an explicit pick is never overridden
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
	// A checkpoint store lets an approval PAUSE the run (persisted, resumable)
	// rather than park a goroutine; without one the gate blocks as before.
	ckptStore := s.checkpointStore()
	if req.ConfirmRisky && req.Trigger != "schedule" && req.Trigger != "workflow" {
		tools = s.wrapConfirm(tools, ckptStore != nil && req.ConversationID != "")
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
	rc.Ctx = withCheckpointStore(rc.Ctx, ckptStore)
	if req.resumeTarget != "" {
		rc.Ctx = withResume(rc.Ctx, req.resumeTarget, req.resumeData)
	}
	// Give this run a budget and a place to record the plan it commits to. Both
	// are read back at finalize: a run that ran out of budget, or that left its
	// own checklist unfinished, must not be filed as a success.
	budget := newRunBudget(einoMaxIters, runMaxTokens, runMaxWall)
	plan := &planTracker{}
	rc.Ctx = withPlanTracker(withBudget(rc.Ctx, budget), plan)
	// Narrow the tool surface to what this run plausibly needs; find_tools opens
	// the rest on demand. Per run, so one conversation unlocking the CSV tools
	// does not make every other conversation pay for them.
	rc.Ctx = withToolGate(rc.Ctx, newToolGate())

	// Record this execution as an auditable run (the automation platform's unit).
	startedAt := time.Now().UnixMilli()
	trigger := req.Trigger
	if trigger == "" {
		trigger = "manual"
	}
	if req.ResumeKey != "" {
		trigger = "resume"
	}
	// Refuse a run that would exceed the caller's rolling cost ceiling. Checked
	// here, after the run context exists but before any model call, so nothing is
	// spent discovering the limit.
	if over := s.quotaExceeded(ctx, req.UserEmail); over != "" {
		s.Msg.Deliver(rc, raw, messages.Chat(messages.RoleAssistant, over, meta), true)
		s.Msg.Deliver(rc, raw, messages.Task("failed", meta), true)
		return db.RunFailed
	}

	runRecID := s.createRun(ctx, req, meta, trigger)
	// Keep the run's record alive while it executes, so a record left at
	// "running" reliably means "abandoned" rather than "we never cleaned up".
	go s.heartbeatRun(ctx, runRecID)
	// Journal the transcript so a mid-run death costs one step, not the whole
	// run. On a resume, seed it with the recovered transcript.
	journal := newRunJournal(s.Cfg.Storage.BaseStoragePath, runRecID, nil)
	rc.Ctx = withJournal(rc.Ctx, journal)
	if req.resumeFrom != nil {
		rc.Ctx = withRunResume(rc.Ctx, req.resumeFrom)
	}

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
		// A question that needs no tools does not need an agent: measured here,
		// the same one-sentence answer costs 14s/3,733 tokens through the agent
		// and 2.6s/651 direct, and 28% of runs make no tool calls at all. The
		// attempt is skipped unless a free heuristic likes the request, and the
		// model can bail out to the agent if it turns out to need tools.
		if !s.tryFastPath(ctx, rc, req, model, modelName, raw) {
			err = s.runEino(ctx, rc, deps, tools, model, modelName, raw)
		}
	}

	s.finish(ctx, rc, meta, req, raw, err)
	status := s.finalizeRun(runRecID, rc, startedAt, req, err, ctx.Err())
	// Compact what this run did into the conversation's memory, BEFORE the
	// journal is settled — the transcript is the only place the tool work exists,
	// and a successful run is about to delete it.
	if t := journal.transcript(); len(t) > 0 {
		s.digestAsync(req.ConversationID, buildDigest(runRecID, req.Message, t), t)
	}
	// The journal exists to rescue a run that died with work behind it. Keep it
	// only when both halves are true — the run ended badly AND it got far enough
	// that resuming beats restarting — and delete it otherwise, so journals do
	// not accumulate for every successful run.
	s.settleJournal(runRecID, journal, status)
	return status
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
	// A run that reached the end of its rope is not a success, however confident
	// its closing paragraph reads. Two independent signals demote it: it ran out
	// of budget, or it never finished the checklist it published. Both are only
	// meaningful for a run that otherwise completed — a failure is already worse.
	var budgetHit string
	var unfinished []string
	// With automatic routing the request no longer says which model ran, so the
	// record has to.
	servingModel, escalated := req.SelectedVersion, false
	if mr, ok := rc.Vars[varModelRouter].(*modelRouter); ok {
		servingModel, escalated = mr.chosen()
	}
	if status == db.RunDone && rc.Ctx != nil {
		budgetHit = budgetFrom(rc.Ctx).exhausted()
		unfinished = planTrackerFrom(rc.Ctx).unfinished()
		if budgetHit != "" || len(unfinished) > 0 {
			status = db.RunPartial
		}
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
		BudgetHit: budgetHit, Unfinished: unfinished,
		Model: servingModel, Escalated: escalated,
	})
	// Advance the scheduled-task circuit breaker. A partial run counts as a
	// success for this purpose: it did work and stopped honestly, which is not
	// the repeated hard failure the breaker exists to catch.
	s.recordTaskOutcome(bg, req.TaskID, req.UserEmail, status != db.RunFailed)
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
		if s.OnEvent != nil {
			s.OnEvent(req.UserEmail, "notification")
		}
	}
	// Signal the run finished so open tabs refresh runs/metrics without waiting
	// for the next poll tick.
	if s.OnEvent != nil && req.UserEmail != "" {
		s.OnEvent(req.UserEmail, "run")
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
	// Filtered in the query, not after: a fixed window over ALL rows fills with
	// tool traffic that is about to be discarded, which left real conversations
	// with as little as one usable turn of history.
	rows, err := s.Msg.Store.GetChatTurns(ctx, convID, 40)
	if err != nil {
		return nil
	}
	out := make([]messages.Message, 0, len(rows)+1)
	// What earlier runs DID goes in front of what they SAID. Chat turns alone
	// carry only the assistant's closing prose, which is a small fraction of the
	// work and the reason follow-up questions used to hit an amnesiac agent.
	if ds, derr := s.Msg.Store.GetRunDigests(ctx, convID); derr == nil {
		if pre := digestPreamble(ds); pre != "" {
			out = append(out, messages.Chat(messages.RoleUser, pre, meta))
		}
	}
	for i := len(rows) - 1; i >= 0; i-- { // newest-first; reverse to chronological
		out = append(out, messages.Chat(rows[i].Role, rows[i].Content, meta))
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
func (s *ChatService) finish(ctx context.Context, rc *agent.RunContext, meta messages.Meta, req ChatRunRequest, raw func(messages.Message), err error) {
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
	case rc.Interrupt != nil && rc.Interrupt.Reason == "confirm":
		// Paused on a danger-tool approval. The Runner already checkpointed the
		// run; remember what it takes to rebuild it so /chat/confirm can resume —
		// including after a control-plane restart. No "done" task: the run is
		// pending, not finished.
		s.persistPendingConfirm(rc, req)
	default:
		s.Msg.Deliver(rc, raw, messages.Task("done", meta), true)
	}
}

// persistPendingConfirm records the interrupted run so it can be resumed later.
func (s *ChatService) persistPendingConfirm(rc *agent.RunContext, req ChatRunRequest) {
	v, ok := rc.Vars[varPendingConfirm]
	if !ok {
		return
	}
	p, ok := v.(pausedRun)
	if !ok {
		return
	}
	req.resumeTarget, req.resumeData = "", nil // never persist a stale decision
	p.Request = req
	savePausedRun(s.Cfg.Storage.BaseStoragePath, p)
}

// PendingConfirm reports the approval a conversation is waiting on, if any.
func (s *ChatService) PendingConfirm(convID string) (tool, summary, target string, ok bool) {
	p, found := loadPausedRun(s.Cfg.Storage.BaseStoragePath, convID)
	if !found {
		return "", "", "", false
	}
	return p.Tool, p.Summary, p.Target, true
}

// ResumeConfirm continues a run that paused on a danger-tool approval. It
// rebuilds the run from the persisted request and resumes the checkpoint with
// the user's decision, streaming events into raw exactly like the original run.
// Returns false when there is nothing pending for this conversation.
func (s *ChatService) ResumeConfirm(ctx context.Context, convID string, approve, always bool, raw func(messages.Message)) bool {
	p, found := loadPausedRun(s.Cfg.Storage.BaseStoragePath, convID)
	if !found {
		return false
	}
	dropPausedRun(s.Cfg.Storage.BaseStoragePath, convID) // one decision per pause

	req := p.Request
	req.ConversationID = convID
	req.resumeTarget = p.Target
	req.resumeData = confirmDecision{Approve: approve, Always: always}
	// Message is empty: the run continues from its checkpoint, it is not a new turn.
	req.Message = ""
	s.Run(ctx, req, raw)
	// If the resumed run finished (rather than pausing again on another approval),
	// its checkpoint is spent — drop it so the directory doesn't accumulate.
	if _, stillPending := loadPausedRun(s.Cfg.Storage.BaseStoragePath, convID); !stillPending {
		if fs, ok := s.checkpointStore().(*fileCheckpointStore); ok {
			fs.drop(convID)
		}
	}
	return true
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
