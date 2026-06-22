package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// confirmHub holds the channels that pause a risky tool call until the user
// approves or rejects it from the UI (POST /chat/confirm).
type confirmHub struct {
	mu      sync.Mutex
	pending map[string]chan bool
}

func newConfirmHub() *confirmHub { return &confirmHub{pending: map[string]chan bool{}} }

func (h *confirmHub) register(id string) chan bool {
	ch := make(chan bool, 1)
	h.mu.Lock()
	h.pending[id] = ch
	h.mu.Unlock()
	return ch
}

func (h *confirmHub) drop(id string) {
	h.mu.Lock()
	delete(h.pending, id)
	h.mu.Unlock()
}

// resolve fulfills a pending confirmation; returns false if the id is unknown
// (already resolved or timed out).
func (h *confirmHub) resolve(id string, approve bool) bool {
	h.mu.Lock()
	ch, ok := h.pending[id]
	if ok {
		delete(h.pending, id)
	}
	h.mu.Unlock()
	if !ok {
		return false
	}
	ch <- approve
	return true
}

// confirmReady lazily initializes the hub (safe under concurrent runs).
func (s *ChatService) confirmReady() *confirmHub {
	s.confirmInit.Do(func() { s.confirms = newConfirmHub() })
	return s.confirms
}

// ResolveConfirm is the API entry point to approve/reject a pending action.
func (s *ChatService) ResolveConfirm(id string, approve bool) bool {
	return s.confirmReady().resolve(id, approve)
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
}

func (g confirmGate) Name() string                   { return g.inner.Name() }
func (g confirmGate) Description() string             { return g.inner.Description() }
func (g confirmGate) Schema() map[string]any          { return g.inner.Schema() }

func (g confirmGate) Invoke(ctx context.Context, args map[string]any) (string, error) {
	emit := agent.EmitFrom(ctx)
	if emit == nil { // no UI channel (e.g. headless) → don't block
		return g.inner.Invoke(ctx, args)
	}
	id := messages.NewID()
	ch := g.hub.register(id)
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
func (s *ChatService) wrapConfirm(tools []agent.BaseTool) []agent.BaseTool {
	hub := s.confirmReady()
	out := make([]agent.BaseTool, len(tools))
	for i, t := range tools {
		if dangerTools[t.Name()] {
			out[i] = confirmGate{inner: t, hub: hub}
		} else {
			out[i] = t
		}
	}
	return out
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
	default:
		return tool
	}
}
