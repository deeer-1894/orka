package middlewares

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/cavis-oss/cavis_core/agent"
	"github.com/cavis-oss/cavis_core/messages"
	"github.com/cavis-oss/cavis_control_layer/llm"
)

// Skill injects a domain skill prompt (loaded from a file under Dir named
// "<skill>.md") into the LLM history as an extra system message. No-op if the
// skill file is absent.
type Skill struct {
	Dir   string
	Skill string
}

func (m *Skill) Name() string { return "skill-mid" }

func (m *Skill) Handle(rc *agent.RunContext, next func(*agent.RunContext) error) error {
	if m.Dir == "" || m.Skill == "" {
		return nil
	}
	path := filepath.Join(m.Dir, m.Skill+".md")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil // best-effort
	}
	prompt := strings.TrimSpace(string(b))
	if prompt == "" {
		return nil
	}
	hist := getHistory(rc)
	// insert skill prompt right after the base system prompt (index 0)
	skillMsg := llm.ChatMessage{Role: llm.RoleSystem, Content: prompt}
	if len(hist) > 0 && hist[0].Role == llm.RoleSystem {
		hist = append(hist[:1], append([]llm.ChatMessage{skillMsg}, hist[1:]...)...)
	} else {
		hist = append([]llm.ChatMessage{skillMsg}, hist...)
	}
	setHistory(rc, hist)
	rc.Emit(messages.New(messages.EventSkill, messages.RoleSystem, rc.Meta))
	return nil
}
