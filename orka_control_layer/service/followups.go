package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

// SuggestFollowups asks the configured model for up to 3 short follow-up questions the
// user is likely to ask next, given the last question + answer. Best-effort: any
// error or unparseable output yields nil (the UI simply shows no suggestions).
func (s *ChatService) SuggestFollowups(ctx context.Context, prompt, answer string) []string {
	models := s.modelsForContext(ctx)
	if models.main == nil || strings.TrimSpace(answer) == "" {
		return nil
	}
	model := models.cfg.Model
	// Keep this minimal: an elaborate prompt makes a reasoning mini model think
	// (and stall) longer. Short instruction + a token cap keeps it a few seconds.
	sys := "Return ONLY a JSON array of 3 short follow-up questions (each under 18 words, same language as the question). No prose, no markdown."
	user := "Q: " + trunc(prompt, 800) + "\nA: " + trunc(answer, 1500)

	// Bound it: follow-ups are a non-blocking nicety, so never let a slow model
	// hold the request — just yield no suggestions on timeout.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := models.main.Chat(llm.WithAgent(ctx, "followups"), llm.Request{
		Model: model,
		// Headroom: a reasoning model may spend tokens thinking before the
		// tiny JSON answer, so a low cap would truncate the answer entirely.
		MaxTokens: 800,
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

func (s *ChatService) SuggestFollowupsForUser(ctx context.Context, owner, version, expectedProfile, prompt, answer string) ([]string, error) {
	if expectedProfile == "" {
		return []string{}, nil
	}
	ctx, err := s.withUserModels(ctx, owner)
	if err != nil {
		return nil, err
	}
	if s.modelsForContext(ctx).profile != expectedProfile {
		return []string{}, nil
	}
	if err = s.modelsForContext(ctx).validateSelection(version); err != nil {
		return nil, err
	}
	ctx = s.withSelectedModel(ctx, version)
	return s.SuggestFollowups(ctx, prompt, answer), nil
}
