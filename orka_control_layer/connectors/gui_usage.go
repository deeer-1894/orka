package connectors

import (
	"context"
	"github.com/orka-oss/orka_control_layer/llm"
)

// GUIUsage is one provider exchange. Nil counts are unknown, never estimates.
type GUIUsage struct {
	CallID           string `json:"call_id"`
	Model            string `json:"model"`
	Known            bool   `json:"known"`
	PromptTokens     *int   `json:"prompt_tokens"`
	CompletionTokens *int   `json:"completion_tokens"`
	TotalTokens      *int   `json:"total_tokens"`
}

type guiUsageLedger struct {
	seen                                      map[string]bool
	calls, unknown, prompt, completion, total int
	complete                                  bool
}

func guiToken(value any) *int {
	number, ok := value.(float64)
	if !ok || number < 0 || number > 1000000000 || number != float64(int(number)) {
		return nil
	}
	result := int(number)
	return &result
}
func (u *guiUsageLedger) add(ctx context.Context, frame map[string]any) {
	if frame["type"] != "usage" {
		return
	}
	id, _ := frame["call_id"].(string)
	if id == "" || len(id) > 300 || u.seen[id] || len(u.seen) >= 100 {
		return
	}
	if u.seen == nil {
		u.seen = make(map[string]bool)
	}
	u.seen[id] = true
	receipt := GUIUsage{CallID: id, PromptTokens: guiToken(frame["prompt_tokens"]),
		CompletionTokens: guiToken(frame["completion_tokens"]), TotalTokens: guiToken(frame["total_tokens"])}
	receipt.Model, _ = frame["model"].(string)
	receipt.Known = receipt.PromptTokens != nil && receipt.CompletionTokens != nil && receipt.TotalTokens != nil
	if known, ok := frame["known"].(bool); ok && !known {
		receipt.Known = false
	}
	u.calls++
	if !receipt.Known {
		u.unknown++
	}
	usage := llm.Usage{Known: receipt.Known, Incomplete: !receipt.Known}
	if receipt.PromptTokens != nil {
		usage.PromptTokens = *receipt.PromptTokens
		u.prompt += *receipt.PromptTokens
	}
	if receipt.CompletionTokens != nil {
		usage.CompletionTokens = *receipt.CompletionTokens
		u.completion += *receipt.CompletionTokens
	}
	if receipt.TotalTokens != nil {
		usage.TotalTokens = *receipt.TotalTokens
		u.total += *receipt.TotalTokens
	}
	llm.ReportExternalUsage(ctx, usage)
}
func (u *guiUsageLedger) summary() map[string]any {
	return map[string]any{"calls": u.calls, "unknown_calls": u.unknown, "known_calls": u.calls - u.unknown,
		"prompt_tokens": u.prompt, "completion_tokens": u.completion, "total_tokens": u.total,
		"complete": u.complete, "note": "Actual provider counts only; unknown/missing exchanges are not zero usage. Terminal totals are not billed twice."}
}

// A terminal message alone is not a complete usage ledger: ensure every
// exchange declared by the executor was received before final settlement.
func (u *guiUsageLedger) finish(frame map[string]any) {
	totals, ok := frame["usage"].(map[string]any)
	if !ok {
		return
	}
	calls := guiToken(totals["calls"])
	u.complete = calls != nil && *calls == u.calls
}
