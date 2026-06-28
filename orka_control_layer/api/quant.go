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

// ListFactors returns the caller's factor library (optional ?status filter via
// body). The pipeline's output, queryable for a UI panel or downstream use.
func (a *API) ListFactors(ctx context.Context, c *app.RequestContext) {
	me := authEmail(c)
	if me == "" {
		fail(c, consts.StatusUnauthorized, "auth required")
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	_ = bind(c, &req)
	factors, err := a.Chat.ListFactors(me, req.Status)
	if err != nil {
		fail(c, consts.StatusInternalServerError, "cannot read factor library")
		return
	}
	ok(c, map[string]any{"factors": factors})
}

// ListPortfolios returns the caller's saved weighted portfolios.
func (a *API) ListPortfolios(ctx context.Context, c *app.RequestContext) {
	me := authEmail(c)
	if me == "" {
		fail(c, consts.StatusUnauthorized, "auth required")
		return
	}
	ports, err := a.Chat.ListPortfolios(me)
	if err != nil {
		fail(c, consts.StatusInternalServerError, "cannot read portfolios")
		return
	}
	ok(c, map[string]any{"portfolios": ports})
}

// SetFactorStatus is the human-review action — approve or reject a pending factor
// from the factor panel.
func (a *API) SetFactorStatus(ctx context.Context, c *app.RequestContext) {
	me := authEmail(c)
	if me == "" {
		fail(c, consts.StatusUnauthorized, "auth required")
		return
	}
	var req struct {
		FactorID string `json:"factor_id"`
		Status   string `json:"status"`
	}
	if err := bind(c, &req); err != nil || req.FactorID == "" || req.Status == "" {
		fail(c, consts.StatusBadRequest, "factor_id and status required")
		return
	}
	if err := a.Chat.SetFactorStatus(me, req.FactorID, req.Status); err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	ok(c, map[string]any{"factor_id": req.FactorID, "status": req.Status})
}
