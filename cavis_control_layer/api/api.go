// Package api holds the Hertz HTTP controllers.
package api

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/cavis-oss/cavis_control_layer/connectors"
	"github.com/cavis-oss/cavis_control_layer/db"
	"github.com/cavis-oss/cavis_control_layer/obs"
	"github.com/cavis-oss/cavis_control_layer/service"
)

// API bundles dependencies shared by all controllers.
type API struct {
	Store       *db.Storage
	Log         *slog.Logger
	Metrics     *obs.Metrics
	Chat        *service.ChatService
	BaseStorage string                   // file storage root
	Directory   connectors.UserDirectory // owner enrichment (cached)
	Secret      string                   // HMAC secret for session tokens
	chunks      *chunkManager            // resumable upload state
}

// authEmail returns the authenticated user's email set by AuthMiddleware.
func authEmail(c *app.RequestContext) string {
	if v, ok := c.Get("email"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// New builds an API controller set.
func New(store *db.Storage, log *slog.Logger, m *obs.Metrics, chat *service.ChatService) *API {
	return &API{Store: store, Log: log, Metrics: m, Chat: chat, chunks: newChunkManager()}
}

// Metrics exposes a runtime metrics snapshot.
func (a *API) MetricsSnapshot(ctx context.Context, c *app.RequestContext) {
	if a.Metrics == nil {
		ok(c, map[string]any{})
		return
	}
	ok(c, a.Metrics.Snapshot())
}

// bind unmarshals the JSON request body into v.
func bind(c *app.RequestContext, v any) error {
	body := c.Request.Body()
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}

// ok writes a success envelope.
func ok(c *app.RequestContext, data any) {
	c.JSON(consts.StatusOK, map[string]any{"code": 0, "msg": "", "data": data})
}

// fail writes an error envelope with the given HTTP status.
func fail(c *app.RequestContext, status int, msg string) {
	c.JSON(status, map[string]any{"code": status, "msg": msg, "data": nil})
}

// Health is the liveness probe.
func (a *API) Health(ctx context.Context, c *app.RequestContext) {
	ok(c, map[string]string{"status": "ok"})
}
