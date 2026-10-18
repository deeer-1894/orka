package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/messages"
)

// budgetCallAccountant adapts actual provider/external attempts to the shared
// BudgetSession. No provider-specific configuration or client ownership lives
// here; all calls inheriting the run context (summary, retry, delegates,
// followups, title and GUI) receive the same parent/daily allowance.
type budgetCallAccountant struct{ session *BudgetSession }

func (a *budgetCallAccountant) Begin(ctx context.Context, req llm.Request) (func(context.Context, llm.Response, error) error, error) {
	callID, source := "llm_"+messages.NewID(), llm.AgentFromContext(ctx)
	if source == "" {
		source = "main"
	}
	reserved, err := estimateInvocationTokens(req, a.session.defaultReservation)
	if err != nil {
		return nil, err
	}
	external, isExternal := llm.ExternalCallFrom(ctx)
	if isExternal {
		callID, source = external.CallID, external.Source
		perStep, err := usageSum(external.PromptTokens, external.MaxCompletionTokens)
		if err != nil || perStep <= 0 || external.MaxSteps <= 0 || external.MaxSteps > int(^uint(0)>>1)/perStep {
			return nil, errors.New("invalid external invocation budget")
		}
		reserved = external.MaxSteps * perStep
	}
	if err := a.session.ReserveUsage(ctx, callID, source, reserved); err != nil {
		return nil, err
	}
	if !isExternal && countsGenerationStep(source) {
		state, err := json.Marshal(req)
		if err != nil {
			return nil, errors.Join(err, a.session.CancelUsage(ctx, callID))
		}
		stepID := fmt.Sprintf("%s:%x", source, sha256.Sum256(state))
		if err := a.session.AdvanceStep(ctx, stepID); err != nil {
			return nil, errors.Join(err, a.session.CancelUsage(ctx, callID))
		}
		if err := a.session.PersistRun(ctx, a.session.runID, "running"); err != nil {
			return nil, errors.Join(err, a.session.CancelUsage(ctx, callID))
		}
	}
	return func(settleCtx context.Context, resp llm.Response, callErr error) error {
		p, c := resp.Usage.PromptTokens, resp.Usage.CompletionTokens
		sum, err := usageSum(p, c)
		if err != nil {
			return err
		}
		if resp.Usage.TotalTokens < 0 {
			return errors.New("negative provider total usage")
		}
		if resp.Usage.TotalTokens > sum {
			c = resp.Usage.TotalTokens - p
		}
		status := UsageUnknown
		// Preserve compatibility with positive pre-presence providers/fakes. External
		// aggregation must explicitly affirm completeness: positive partial usage
		// never releases the unreported part of a GUI invocation's reservation.
		known := !resp.Usage.Incomplete && (resp.Usage.Known || (!isExternal && (p+c > 0)))
		if known {
			status = UsageKnown
		}
		return a.session.SettleUsage(settleCtx, callID, UsageSettlement{Status: status, PromptTokens: p, CompletionTokens: c})
	}, nil
}

// EstimatePromptTokens intentionally uses a conservative byte-based estimate,
// including tool schemas and fixed image headroom. It is not authoritative
// usage. Provider totals always replace it, and unknown usage retains it.
func EstimatePromptTokens(req llm.Request) (int, error) {
	payload := struct {
		Messages []llm.ChatMessage
		Tools    []llm.ToolSpec
	}{req.Messages, req.Tools}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("estimate model request: %w", err)
	}
	total := len(raw)
	for _, m := range req.Messages {
		if len(m.Images) > int(^uint(0)>>1)/4096 {
			return 0, errors.New("image token estimate overflows")
		}
		total, err = usageSum(total, len(m.Images)*4096)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}
func estimateInvocationTokens(req llm.Request, fallbackCompletion int) (int, error) {
	if req.MaxTokens < 0 {
		return 0, errors.New("negative model output allowance")
	}
	prompt, err := EstimatePromptTokens(req)
	if err != nil {
		return 0, err
	}
	completion := req.MaxTokens
	if completion == 0 {
		completion = fallbackCompletion
	}
	return usageSum(prompt, completion)
}

var _ llm.CallAccountant = (*budgetCallAccountant)(nil)

func countsGenerationStep(source string) bool {
	switch source {
	case "summary", "summarizer", "title", "run-digest", "followups", "probe", "attachment-vlm":
		return false
	}
	return true
}

// boundedDirectRequest applies the same saved output policy as the agent adapter
// before the accountant estimates/reserves and the provider receives the request.
func boundedDirectRequest(ctx context.Context, req llm.Request) llm.Request {
	limits := agentCallLimits(req.Model)
	if limits.ForContext != nil {
		limits = limits.ForContext(ctx, req.Model)
	}
	cap := limits.MaxTokens
	first := true
	for _, msg := range req.Messages {
		if msg.Role == llm.RoleAssistant || msg.Role == llm.RoleTool {
			first = false
			break
		}
	}
	if first && limits.FirstMaxTokens > 0 && (cap <= 0 || limits.FirstMaxTokens < cap) {
		cap = limits.FirstMaxTokens
	}
	if cap > 0 && (req.MaxTokens <= 0 || req.MaxTokens > cap) {
		req.MaxTokens = cap
	}
	if req.ReasoningEffort == "" && limits.ReasoningEffort != nil {
		req.ReasoningEffort = limits.ReasoningEffort(req.Model)
	}
	return req
}
