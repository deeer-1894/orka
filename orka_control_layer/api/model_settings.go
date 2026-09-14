package api

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/orka-oss/orka_control_layer/modelsettings"
)

func (a *API) settingsStore(c *app.RequestContext) *modelsettings.Store {
	if authEmail(c) == "" {
		fail(c, consts.StatusUnauthorized, "authentication required")
		return nil
	}
	if a.Chat == nil || a.Chat.ModelSettings == nil {
		fail(c, consts.StatusServiceUnavailable, "model settings unavailable")
		return nil
	}
	c.Response.Header.Set("Cache-Control", "no-store")
	return a.Chat.ModelSettings
}

func settingsError(c *app.RequestContext, err error) {
	status := consts.StatusBadRequest
	if errors.Is(err, modelsettings.ErrStorage) {
		status = consts.StatusServiceUnavailable
	}
	fail(c, status, err.Error())
}

func (a *API) GetModelSettings(_ context.Context, c *app.RequestContext) {
	store := a.settingsStore(c)
	if store == nil {
		return
	}
	cfg, _, err := store.Get(authEmail(c))
	if err != nil {
		settingsError(c, err)
		return
	}
	ok(c, cfg.Public())
}

func (a *API) SaveModelSettings(_ context.Context, c *app.RequestContext) {
	store := a.settingsStore(c)
	if store == nil {
		return
	}
	var req struct {
		modelsettings.Config
		APIKey      string `json:"api_key"`
		LegacyModel string `json:"model"`
	}
	if len(c.Request.Body()) > 128<<10 {
		fail(c, consts.StatusRequestEntityTooLarge, "request too large")
		return
	}
	if bind(c, &req) != nil {
		fail(c, consts.StatusBadRequest, "invalid model settings request")
		return
	}
	req.Config.Model = req.LegacyModel // accept old imported profiles, never expose tiers
	cfg, err := store.Save(authEmail(c), req.Config, req.APIKey)
	if err != nil {
		settingsError(c, err)
		return
	}
	ok(c, cfg.Public())
}

func (a *API) DiscoverModels(ctx context.Context, c *app.RequestContext) {
	store := a.settingsStore(c)
	if store == nil {
		return
	}
	var req struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
	}
	if len(c.Request.Body()) > 128<<10 {
		fail(c, consts.StatusRequestEntityTooLarge, "request too large")
		return
	}
	if bind(c, &req) != nil {
		fail(c, consts.StatusBadRequest, "invalid model discovery request")
		return
	}
	models, err := store.Discover(ctx, authEmail(c), req.BaseURL, req.APIKey)
	if err != nil {
		settingsError(c, err)
		return
	}
	ok(c, map[string]any{"models": models})
}
