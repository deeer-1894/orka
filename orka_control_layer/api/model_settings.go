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
		ProfileID string `json:"profile_id"`
		Protocol  string `json:"protocol"`
		Provider  string `json:"provider"`
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
	}
	if len(c.Request.Body()) > 128<<10 {
		fail(c, consts.StatusRequestEntityTooLarge, "request too large")
		return
	}
	if bind(c, &req) != nil {
		fail(c, consts.StatusBadRequest, "invalid model discovery request")
		return
	}
	if err := modelsettings.RequireSupportedProtocol(req.Protocol); err != nil {
		settingsError(c, err)
		return
	}
	var result modelsettings.DiscoverResult
	var err error
	if req.ProfileID != "" {
		result, err = store.DiscoverProfile(ctx, authEmail(c), req.ProfileID, req.BaseURL, req.APIKey, req.Protocol)
	} else {
		result, err = store.DiscoverWithMetadata(ctx, authEmail(c), req.BaseURL, req.APIKey)
	}
	if err != nil {
		settingsError(c, err)
		return
	}
	ok(c, result)
}

// Named profiles share owner authentication and private storage with legacy settings.
func (a *API) GetModelProfiles(_ context.Context, c *app.RequestContext) {
	store := a.settingsStore(c)
	if store == nil {
		return
	}
	profiles, err := store.GetProfiles(authEmail(c))
	if err != nil {
		settingsError(c, err)
		return
	}
	ok(c, profiles.Public())
}
func (a *API) SaveModelProfiles(_ context.Context, c *app.RequestContext) {
	store := a.settingsStore(c)
	if store == nil {
		return
	}
	if len(c.Request.Body()) > 128<<10 {
		fail(c, consts.StatusRequestEntityTooLarge, "request too large")
		return
	}
	var req struct {
		Profiles []struct {
			modelsettings.Config
			APIKey string `json:"api_key"`
		} `json:"profiles"`
		ActiveProfileID string `json:"active_profile_id"`
	}
	if bind(c, &req) != nil || req.Profiles == nil {
		fail(c, consts.StatusBadRequest, "invalid model profiles request")
		return
	}
	p := modelsettings.Profiles{Profiles: []modelsettings.Config{}, ActiveProfileID: req.ActiveProfileID}
	keys := map[string]string{}
	for _, profile := range req.Profiles {
		p.Profiles = append(p.Profiles, profile.Config)
		keys[profile.ID] = profile.APIKey
	}
	saved, err := store.SaveProfiles(authEmail(c), p, keys)
	if err != nil {
		settingsError(c, err)
		return
	}
	ok(c, saved.Public())
}
func (a *API) ProbeModelProfile(ctx context.Context, c *app.RequestContext) {
	store := a.settingsStore(c)
	if store == nil {
		return
	}
	if len(c.Request.Body()) > 128<<10 {
		fail(c, consts.StatusRequestEntityTooLarge, "request too large")
		return
	}
	var req struct {
		ProfileID    string   `json:"profile_id"`
		Model        string   `json:"model"`
		Capabilities []string `json:"capabilities"`
	}
	if bind(c, &req) != nil {
		fail(c, consts.StatusBadRequest, "invalid model probe request")
		return
	}
	if len(req.Capabilities) == 0 {
		fail(c, consts.StatusBadRequest, "choose at least one capability to probe")
		return
	}
	budgetCtx, cancelBudget, budgetErr := a.Chat.AuxiliaryBudgetContext(ctx, authEmail(c), "probe")
	if budgetErr != nil {
		fail(c, consts.StatusServiceUnavailable, "用量账本暂不可用，未执行模型探测")
		return
	}
	defer cancelBudget()
	result, err := store.Probe(budgetCtx, authEmail(c), req.ProfileID, req.Model, req.Capabilities)
	if err != nil {
		settingsError(c, err)
		return
	}
	ok(c, result)
}
