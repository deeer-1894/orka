package middlewares

import (
	"github.com/cavis-oss/cavis_core/agent"
	"github.com/cavis-oss/cavis_control_layer/llm"
)

// Memory trims the LLM history to bound context size, always keeping the
// leading system prompt(s) and the most recent messages.
type Memory struct {
	MaxMessages int
}

func (m *Memory) Name() string { return "memory-mid" }

func (m *Memory) Handle(rc *agent.RunContext, next func(*agent.RunContext) error) error {
	max := m.MaxMessages
	if max <= 0 {
		max = 50
	}
	hist := getHistory(rc)
	if trimmed, changed := trimHistory(hist, max); changed {
		setHistory(rc, trimmed)
	}
	return nil
}

// trimHistory bounds a chat history to ~max messages, always preserving the
// leading system prompt(s). It is tool-call safe: the kept tail never begins
// with an orphan tool message (a tool reply whose assistant tool_calls were
// trimmed away), which strict providers reject. Returns the (possibly) trimmed
// slice and whether anything was dropped.
func trimHistory(hist []llm.ChatMessage, max int) ([]llm.ChatMessage, bool) {
	if max < 1 || len(hist) <= max {
		return hist, false
	}
	head := 0
	for head < len(hist) && hist[head].Role == llm.RoleSystem {
		head++
	}
	keepTail := max - head
	if keepTail < 1 {
		keepTail = 1
	}
	start := len(hist) - keepTail
	if start < head {
		start = head
	}
	// Never start the tail on a tool reply with no preceding tool_calls.
	for start < len(hist) && hist[start].Role == llm.RoleTool {
		start++
	}
	if start <= head {
		return hist, false // nothing meaningful to trim
	}
	trimmed := make([]llm.ChatMessage, 0, head+(len(hist)-start))
	trimmed = append(trimmed, hist[:head]...)
	trimmed = append(trimmed, hist[start:]...)
	return trimmed, true
}
