package llm

import (
	"context"
	"testing"
)

type externalUsageCounter struct{ prompt, completion int }

func (s *externalUsageCounter) AddUsage(p, c int) { s.prompt += p; s.completion += c }
func TestExternalUsageSharesExistingSink(t *testing.T) {
	sink := &externalUsageCounter{}
	ctx := WithUsageSink(context.Background(), sink)
	ReportExternalUsage(ctx, Usage{PromptTokens: 12, CompletionTokens: 8, TotalTokens: 20, ReasoningTokens: 3})
	if sink.prompt != 12 || sink.completion != 8 {
		t.Fatal("external cost absent or double counted")
	}
	ReportExternalUsage(context.Background(), Usage{TotalTokens: 2})
	ReportExternalUsage(ctx, Usage{PromptTokens: -1, CompletionTokens: -2})
	if sink.prompt != 12 || sink.completion != 8 {
		t.Fatal("negative usage reduced ledger")
	}
	ReportExternalUsage(ctx, Usage{TotalTokens: 5})
	if sink.prompt+sink.completion != 25 {
		t.Fatal("total-only usage lost")
	}
}
