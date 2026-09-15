package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/orka-oss/orka_control_layer/db"
	"strings"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

// generateFollowups asks the configured model for up to 3 short follow-up questions the
// user is likely to ask next, given the last question + answer. Best-effort: any
// error or unparseable output yields nil (the UI simply shows no suggestions).
func (s *ChatService) generateFollowups(ctx context.Context, prompt, answer string) []string {
	models := s.modelsForContext(ctx)
	if models.client == nil || strings.TrimSpace(answer) == "" {
		return nil
	}
	model := models.cfg.Model
	// Keep this minimal: an elaborate prompt makes a reasoning model think
	// (and stall) longer. Short instruction + a token cap keeps it a few seconds.
	sys := "Return ONLY a JSON array of 3 short follow-up questions (each under 18 words, same language as the question). No prose, no markdown."
	user := "Q: " + trunc(prompt, 800) + "\nA: " + trunc(answer, 1500)

	// Bound it: follow-ups are a non-blocking nicety, so never let a slow model
	// hold the request — just yield no suggestions on timeout.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := models.client.Chat(llm.WithAgent(ctx, "followups"), boundedDirectRequest(ctx, llm.Request{
		Model: model,
		// Headroom: a reasoning model may spend tokens thinking before the
		// tiny JSON answer, so a low cap would truncate the answer entirely.
		MaxTokens: 800,
		Messages: []llm.ChatMessage{
			{Role: llm.RoleSystem, Content: sys},
			{Role: llm.RoleUser, Content: user},
		},
	}))
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
			cleaned = append(cleaned, trunc(q, 256))
		}
		if len(cleaned) == 3 {
			break
		}
	}
	return cleaned
}

var (
	ErrFollowupIdentity   = errors.New("conversation_id, run_id and model_profile are required")
	ErrFollowupRunPending = errors.New("run is not finalized; retry after its terminal record is saved")
	ErrFollowupStorage    = errors.New("followup run storage unavailable")
)

// SuggestFollowupsForRun derives inputs from an authenticated, persisted run.
// Client-provided answer/prompt/model are never used for paid generation.
func (s *ChatService) SuggestFollowupsForRun(ctx context.Context, owner, conversationID, runID, expectedProfile string) ([]string, error) {
	for _, value := range []string{owner, conversationID, runID, expectedProfile} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return nil, ErrFollowupIdentity
		}
	}
	if s.Msg == nil || s.Msg.Store == nil || s.Msg.Store.Runs == nil {
		return nil, ErrFollowupStorage
	}
	run, err := s.Msg.Store.GetRun(ctx, runID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, db.ErrNotFound
	}
	if err != nil {
		return nil, ErrFollowupStorage
	}
	if run.OwnerEmail != owner || run.ConversationID != conversationID || run.RunID != runID {
		return nil, db.ErrNotFound
	}
	if run.Status == db.RunRunning {
		return nil, ErrFollowupRunPending
	}
	if (run.Status != db.RunDone && run.Status != db.RunPartial) || strings.TrimSpace(run.Output) == "" || strings.TrimSpace(run.Model) == "" {
		return []string{}, nil
	}
	// Resolve afresh rather than accepting a model snapshot from an unrelated run.
	models, err := s.resolveModels(owner)
	if err != nil {
		return nil, err
	}
	if models.profile != expectedProfile {
		return []string{}, nil
	}
	if err = models.validateSelection(run.Model); err != nil {
		return nil, err
	}
	ctx = s.withSelectedModel(context.WithValue(ctx, modelSnapshotKey{}, models), run.Model)
	return s.followups.get(ctx, followupKey{owner, conversationID, runID}, expectedProfile, func(generationCtx context.Context) ([]string, error) {
		budgetCtx, cancel, err := s.AuxiliaryBudgetContextForRun(generationCtx, owner, "followups", conversationID, runID)
		if err != nil {
			return nil, err
		}
		defer cancel()
		return s.generateFollowups(budgetCtx, run.Prompt, run.Output), nil
	})
}
