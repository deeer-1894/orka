package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cavis-oss/cavis_core/agent"
	cp "github.com/cavis-oss/cavis_core/checkpoint"
	"github.com/cavis-oss/cavis_core/config"
	"github.com/cavis-oss/cavis_core/messages"
	"github.com/cavis-oss/cavis_core/trace"

	"github.com/cavis-oss/cavis_control_layer/checkpoint"
	"github.com/cavis-oss/cavis_control_layer/llm"
	"github.com/cavis-oss/cavis_control_layer/message_utils"
	"github.com/cavis-oss/cavis_control_layer/obs"
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
func (s *ChatService) Run(parent context.Context, req ChatRunRequest, raw func(messages.Message)) {
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
		"run_mode":        s.Cfg.Agent.RunMode,
		"resume":          boolStr(req.ResumeKey != ""),
	})
	defer endSpan()
	ctx = spanCtx

	deps := PipelineDeps{LLM: model, Model: modelName, Metrics: s.Metrics}
	pipeline := BuildPipeline(SceneSimple, deps)
	runner := RunnerForMode(s.Cfg.Agent.RunMode, pipeline...)

	tools, cleanup, err := s.ToolsFor(ctx, req)
	if err != nil && s.Log != nil {
		s.Log.Warn("tools provider degraded", "trace_id", traceID, "err", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	rc := &agent.RunContext{Ctx: ctx, Vars: map[string]any{}, Meta: meta, Tools: tools}
	rc.Send = func(m messages.Message) { s.Msg.Deliver(rc, raw, m, true) }
	// Expose the emit sink on the context so tools (e.g. run_agent) can stream
	// their own events (browser/...) into the same SSE stream.
	rc.Ctx = agent.WithEmit(ctx, rc.Send)

	if req.ResumeKey != "" {
		err = s.resume(ctx, runner, rc, req, raw)
		if err != nil {
			return // resume() already emitted the failure event
		}
	} else {
		rc.Messages = []messages.Message{messages.Chat(messages.RoleUser, req.Message, meta)}
		s.Msg.Deliver(rc, raw, messages.Task("start", meta), true)
		err = runner.Run(rc)
	}

	s.finish(ctx, rc, meta, raw, err)
}

// resume claims the checkpoint (idempotent) and continues the run.
func (s *ChatService) resume(ctx context.Context, runner agent.Runner, rc *agent.RunContext, req ChatRunRequest, raw func(messages.Message)) error {
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
	return runner.ResumeWithParams(rc, req.ResumeKey, req.Message)
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

func taskFailed(meta messages.Meta, reason string) messages.Message {
	m := messages.Task("failed", meta)
	m.Content = reason
	return m
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
