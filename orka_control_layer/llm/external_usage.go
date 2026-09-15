package llm

import "context"

// ReportExternalUsage books a GUI/provider usage event in the existing run sink.
// Call once per provider exchange, never again for cumulative terminal totals.
// Reasoning is already part of completion. A provider's total-only/unattributed
// remainder is booked as completion so it cannot evade the shared token budget.
func ReportExternalUsage(ctx context.Context, usage Usage) {
	if ctx == nil {
		return
	}
	// Active external calls validate raw counts before legacy normalization.
	// Bad provider data must retain the reservation, never become a known zero.
	if collector, ok := usageSinkFrom(ctx).(*externalUsageCollector); ok {
		collector.report(usage)
		return
	}
	prompt, completion := max(0, usage.PromptTokens), max(0, usage.CompletionTokens)
	completion = min(completion, int(^uint(0)>>1)-prompt)
	if usage.TotalTokens > prompt+completion {
		completion = usage.TotalTokens - prompt
	}
	if s := usageSinkFrom(ctx); s != nil {
		s.AddUsage(prompt, completion)
	}
}
