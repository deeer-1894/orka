package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/compose"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// pending is one paused tool call awaiting the user's decision.
type pending struct {
	ch   chan bool
	conv string
	tool string
}

// confirmHub pauses risky tool calls until the user decides (POST /chat/confirm).
// "always this session" records the tool as pre-approved for that conversation,
// so subsequent calls of the same tool skip the gate (Claude-Code-style).
type confirmHub struct {
	mu      sync.Mutex
	pending map[string]*pending
	allowed map[string]map[string]bool // conv -> tool -> approved for the session
	store   string                     // storage root; "" disables persistence
}

func newConfirmHub() *confirmHub {
	return &confirmHub{pending: map[string]*pending{}, allowed: map[string]map[string]bool{}}
}

func (h *confirmHub) register(id, conv, tool string) chan bool {
	ch := make(chan bool, 1)
	h.mu.Lock()
	h.pending[id] = &pending{ch: ch, conv: conv, tool: tool}
	h.mu.Unlock()
	return ch
}

func (h *confirmHub) drop(id string) {
	h.mu.Lock()
	delete(h.pending, id)
	h.mu.Unlock()
}

// grant records an "always allow" decision for conv+tool and persists it.
func (h *confirmHub) grant(conv, tool string) {
	if conv == "" {
		return
	}
	h.mu.Lock()
	if h.allowed[conv] == nil {
		h.allowed[conv] = map[string]bool{}
	}
	h.allowed[conv][tool] = true
	h.mu.Unlock()
	h.persist()
}

func (h *confirmHub) isAllowed(conv, tool string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.allowed[conv][tool]
}

// resolve fulfills a pending confirmation; when approve+always, the tool becomes
// pre-approved for that conversation. Returns false if the id is unknown.
func (h *confirmHub) resolve(id string, approve, always bool) bool {
	h.mu.Lock()
	p, ok := h.pending[id]
	if ok {
		delete(h.pending, id)
		if approve && always && p.conv != "" {
			if h.allowed[p.conv] == nil {
				h.allowed[p.conv] = map[string]bool{}
			}
			h.allowed[p.conv][p.tool] = true
		}
	}
	h.mu.Unlock()
	if !ok {
		return false
	}
	if approve && always {
		h.persist() // survive a control-plane restart
	}
	p.ch <- approve
	return true
}

// confirmReady lazily initializes the hub (safe under concurrent runs) and
// restores any "always allow" grants from disk, so a control-plane restart does
// not silently revoke a decision the user already made.
func (s *ChatService) confirmReady() *confirmHub {
	s.confirmInit.Do(func() {
		s.confirms = newConfirmHub()
		s.confirms.store = s.Cfg.Storage.BaseStoragePath
		s.confirms.load()
	})
	return s.confirms
}

// grantsPath is where per-conversation "always allow" decisions are persisted.
// One file for the whole install (grants are keyed by conversation, which is
// already user-scoped) kept beside the other control-plane state.
func grantsPath(baseStorage string) string {
	if baseStorage == "" {
		return ""
	}
	return filepath.Join(baseStorage, ".orka_confirm_grants.json")
}

// load restores persisted grants; a missing/corrupt file just means no grants.
func (h *confirmHub) load() {
	p := grantsPath(h.store)
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var saved map[string]map[string]bool
	if json.Unmarshal(b, &saved) != nil || saved == nil {
		return
	}
	h.mu.Lock()
	h.allowed = saved
	h.mu.Unlock()
}

// persist writes the grants out (best-effort; caller holds no lock).
func (h *confirmHub) persist() {
	p := grantsPath(h.store)
	if p == "" {
		return
	}
	h.mu.Lock()
	b, err := json.Marshal(h.allowed)
	h.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, b, 0o600)
}

// ResolveConfirm is the API entry point to approve/reject a pending action.
// always = approve for the rest of this conversation.
func (s *ChatService) ResolveConfirm(id string, approve, always bool) bool {
	return s.confirmReady().resolve(id, approve, always)
}

// confirmTimeout bounds how long a tool call waits for the user before it is
// auto-skipped, so an unattended run can't block a tool slot forever.
const confirmTimeout = 5 * time.Minute

// confirmGate wraps a tool so that, before it runs, the UI is asked to approve
// the side-effecting action. Rejection (or timeout) skips the call with a
// non-fatal message instead of executing it.
type confirmGate struct {
	inner agent.BaseTool
	hub   *confirmHub
	// interruptible: when the run has a checkpoint store, pause via a real ADK
	// interrupt (the run is persisted and released) instead of parking a
	// goroutine on a channel for up to five minutes.
	interruptible bool
}

// confirmDecision is what the user's answer carries back into the resumed tool.
type confirmDecision struct {
	Approve bool `json:"approve"`
	Always  bool `json:"always"`
}

// ConfirmInterrupt is the payload surfaced when a danger tool pauses a run. It
// is what the UI renders and what /chat/confirm resolves.
type ConfirmInterrupt struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
}

func (g confirmGate) Name() string           { return g.inner.Name() }
func (g confirmGate) Description() string    { return g.inner.Description() }
func (g confirmGate) Schema() map[string]any { return g.inner.Schema() }

func (g confirmGate) Invoke(ctx context.Context, args map[string]any) (string, error) {
	emit := agent.EmitFrom(ctx)
	conv := agent.MetaFrom(ctx).ConversationID
	if emit == nil { // no UI channel (e.g. headless) → don't block
		return g.inner.Invoke(ctx, args)
	}
	if g.hub.isAllowed(conv, g.inner.Name()) { // already approved for this session
		return g.inner.Invoke(ctx, args)
	}
	if !needsConfirm(g.inner.Name(), args) {
		return g.inner.Invoke(ctx, args)
	}

	// --- native interrupt/resume path -------------------------------------
	// On the way back in, the user's decision arrives as resume data addressed
	// to THIS tool call; act on it and finish. On the way out, pause the run
	// (persisted by the Runner) instead of blocking a goroutine.
	if g.interruptible {
		isResume, hasData, dec := compose.GetResumeContext[confirmDecision](ctx)
		if isResume && hasData {
			if dec.Always && conv != "" {
				g.hub.grant(conv, g.inner.Name())
			}
			if !dec.Approve {
				return "用户拒绝了该操作,已跳过。", nil
			}
			return g.inner.Invoke(ctx, args)
		}
		if isResume { // targeted at us but with no decision → treat as declined
			return "未收到确认结果,已跳过该操作。", nil
		}
		return "", compose.Interrupt(ctx, ConfirmInterrupt{
			Tool:    g.inner.Name(),
			Summary: summarizeAction(g.inner.Name(), args),
		})
	}

	// --- fallback: block until answered (headless / no checkpoint store) ----
	id := messages.NewID()
	ch := g.hub.register(id, conv, g.inner.Name())
	defer g.hub.drop(id)

	emit(messages.Confirm(messages.ConfirmRequest{
		ID:      id,
		Tool:    g.inner.Name(),
		Summary: summarizeAction(g.inner.Name(), args),
	}, agent.MetaFrom(ctx)))

	select {
	case approve := <-ch:
		if !approve {
			return "用户拒绝了该操作,已跳过。", nil
		}
		return g.inner.Invoke(ctx, args)
	case <-time.After(confirmTimeout):
		return "等待用户确认超时,已跳过该操作。", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// wrapConfirm gates the side-effecting tools (shell / browser / network /
// code-exec) behind a confirmation, leaving read-only tools untouched.
func (s *ChatService) wrapConfirm(tools []agent.BaseTool, interruptible bool) []agent.BaseTool {
	hub := s.confirmReady()
	out := make([]agent.BaseTool, len(tools))
	for i, t := range tools {
		if dangerTools[t.Name()] {
			out[i] = confirmGate{inner: t, hub: hub, interruptible: interruptible}
		} else {
			out[i] = t
		}
	}
	return out
}

// needsConfirm decides from the ACTUAL call, not just the tool's name.
//
// dangerTools is a name list, and for http_request the name is the wrong unit:
// the tool covers both a read and a write, and a GET is a read. The gate fired
// on `GET https://pkg.go.dev/...` during a research run — a fetch of a public
// documentation page, indistinguishable in effect from fetch_url, which is not
// gated at all. Asking a human to approve that teaches them to approve without
// reading, which is how a gate stops protecting anything.
//
// Only the method is inspected. Everything else on the list runs arbitrary code
// or commits a consequential change however it is called, so it stays gated.
func needsConfirm(tool string, args map[string]any) bool {
	if tool != "http_request" {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(asStr(args["method"]))) {
	case "", "GET", "HEAD", "OPTIONS": // "" is the tool's documented GET default
		return false
	default: // POST/PUT/PATCH/DELETE can change state on the far end
		return true
	}
}

// summarizeAction renders a short, human line describing what the tool is about
// to do, so the user can decide without reading raw JSON args.
func summarizeAction(tool string, args map[string]any) string {
	s := func(k string) string {
		if v, ok := args[k]; ok && v != nil {
			return strings.TrimSpace(fmt.Sprint(v))
		}
		return ""
	}
	switch tool {
	case "shell":
		return "在工作区执行命令: " + trunc(s("command")+s("cmd"), 200)
	case "run_agent":
		return "用浏览器执行: " + trunc(s("instruction"), 200)
	case "http_request":
		m := s("method")
		if m == "" {
			m = "GET"
		}
		return fmt.Sprintf("发起网络请求 %s %s", m, trunc(s("url"), 160))
	case "python":
		if p := s("path"); p != "" {
			return "运行 Python 脚本: " + p
		}
		return "运行 Python 代码: " + trunc(s("code"), 160)
	case "ingest_factor":
		name := s("name")
		if name == "" {
			if f, ok := args["factor"].(map[string]any); ok {
				if v, ok := f["name"]; ok {
					name = fmt.Sprint(v)
				}
			}
		}
		return "把因子录入因子库: " + trunc(name, 120)
	default:
		return tool
	}
}
