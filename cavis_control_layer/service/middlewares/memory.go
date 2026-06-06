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
	if len(hist) <= max {
		return nil
	}
	// preserve leading system messages
	head := 0
	for head < len(hist) && hist[head].Role == llm.RoleSystem {
		head++
	}
	keepTail := max - head
	if keepTail < 1 {
		keepTail = 1
	}
	trimmed := make([]llm.ChatMessage, 0, head+keepTail)
	trimmed = append(trimmed, hist[:head]...)
	trimmed = append(trimmed, hist[len(hist)-keepTail:]...)
	setHistory(rc, trimmed)
	return nil
}
