package api

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/orka-oss/orka_control_layer/db"
)

// GetRunBudget serves GET /run/:run_id/budget under the existing authenticated
// API prefix. Storage lookups include owner; knowing another run ID is useless.
func (a *API) GetRunBudget(ctx context.Context, c *app.RequestContext) {
	owner := authEmail(c)
	if owner == "" {
		fail(c, consts.StatusUnauthorized, "authentication required")
		return
	}
	runID := c.Param("run_id")
	if runID == "" {
		fail(c, consts.StatusBadRequest, "run_id is required")
		return
	}
	if a.Chat == nil {
		fail(c, consts.StatusServiceUnavailable, "budget unavailable")
		return
	}
	snapshot, err := a.Chat.RunBudgetSnapshot(ctx, owner, runID)
	if errors.Is(err, db.ErrNotFound) {
		fail(c, consts.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		fail(c, consts.StatusServiceUnavailable, "用量账本暂不可用")
		return
	}
	c.Response.Header.Set("Cache-Control", "no-store")
	ok(c, snapshot)
}
