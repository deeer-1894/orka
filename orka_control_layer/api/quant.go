package api

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// RunFactorPipeline kicks off the research-report → quant-factor pipeline over
// every report in the caller's workspace reports/ folder (one isolated run per
// report). This is the entry point a daily scheduled task hits. Returns the
// conversation ids started; observe each in run history.
func (a *API) RunFactorPipeline(ctx context.Context, c *app.RequestContext) {
	me := authEmail(c)
	if me == "" {
		fail(c, consts.StatusUnauthorized, "auth required")
		return
	}
	// Discover up front so the caller learns how many reports will be processed,
	// then run the (slow, multi-step) batch detached so the HTTP call returns
	// immediately — observe progress in run history.
	reports := a.Chat.DiscoverReports(me)
	go a.Chat.RunFactorPipeline(context.Background(), me)
	ok(c, map[string]any{"started": len(reports), "reports": reports})
}
