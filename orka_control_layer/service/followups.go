package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

// SuggestFollowups asks the mini model for up to 3 short follow-up questions the
// user is likely to ask next, given the last question + answer. Best-effort: any
// error or unparseable output yields nil (the UI simply shows no suggestions).
func (s *ChatService) SuggestFollowups(ctx context.Context, prompt, answer string) []string {
	if s.Mini == nil || strings.TrimSpace(answer) == "" {
		return nil
	}
	model := s.Cfg.LLM.MiniModel
	if model == "" {
		model = s.Cfg.LLM.Model
	}
	// Keep this minimal: an elaborate prompt makes a reasoning mini model think
	// (and stall) longer. Short instruction + a token cap keeps it a few seconds.
	sys := "Return ONLY a JSON array of 3 short follow-up questions (each under 18 words, same language as the question). No prose, no markdown."
	user := "Q: " + trunc(prompt, 800) + "\nA: " + trunc(answer, 1500)

	// Bound it: follow-ups are a non-blocking nicety, so never let a slow model
	// hold the request — just yield no suggestions on timeout. The fast mini model
	// (mimo-v2.5) usually answers in a few seconds; 15s absorbs its variance.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := s.Mini.Chat(ctx, llm.Request{
		Model:           model,
		DisableThinking: true,
		MaxTokens:       800,
		Messages: []llm.ChatMessage{
			{Role: llm.RoleSystem, Content: sys},
			{Role: llm.RoleUser, Content: user},
		},
	})
	if err != nil {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(extractJSON(resp.Content)), &out); err != nil {
		return nil
	}
	cleaned := make([]string, 0, 3)
	for _, q := range out {
		if q = strings.TrimSpace(q); q != "" {
			cleaned = append(cleaned, q)
		}
		if len(cleaned) == 3 {
			break
		}
	}
	return cleaned
}
