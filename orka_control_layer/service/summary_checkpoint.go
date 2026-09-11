package service

import (
	"context"
	"errors"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/schema"
)

// An incomplete checkpoint must not replace the only complete history.
// bestEffort leaves the original state intact when this finalizer fails.
func finalizeSummary(ctx context.Context, original []*schema.Message, summary *schema.Message) ([]*schema.Message, error) {
	if summary == nil || (summary.ResponseMeta != nil && summary.ResponseMeta.FinishReason == "length") {
		return nil, errors.New("incomplete summary")
	}
	return summarization.DefaultFinalize(ctx, original, summary)
}
