package api

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_control_layer/llm"
)

// settings.go — the endpoint and models, editable without touching a file.
//
// Changing where this points used to mean editing config.yaml or .env and
// restarting. That is the friction the panel removes; the settings live in the
// user's own JSON file (see config.SettingsPath) and take effect immediately.
//
// The API key is write-only throughout. It goes in, it is never returned, and
// an empty value on save means "keep the one already stored" — so saving a
// changed base URL does not require re-typing the key, and a client that never
// received it cannot accidentally erase it.

// GetSettings returns the saved settings with the API key redacted.
func (a *API) GetSettings(_ context.Context, c *app.RequestContext) {
	saved, err := config.LoadSettings(config.SettingsPath())
	if err != nil {
		a.Log.Error("read settings", "err", err)
		fail(c, consts.StatusInternalServerError, "读取设置失败: "+err.Error())
		return
	}
	redacted, hasKey := saved.LLM.Redacted()
	// Report what is EFFECTIVE too, not just what was saved: most fields fall
	// back to config.yaml or the environment, and a panel showing empty boxes
	// for a working deployment would be actively misleading.
	eff := a.Chat.EffectiveLLM()
	ok(c, map[string]any{
		"saved":   redacted,
		"has_key": hasKey,
		"effective": map[string]any{
			"base_url":           eff.OpenAIBaseURL,
			"models":             eff.SelectableModels(),
			"max_tokens":         eff.MaxTokens,
			"reasoning":          eff.Reasoning,
			"reasoning_by_model": eff.ReasoningByModel,
			"has_key":            strings.TrimSpace(eff.OpenAIAPIKey) != "",
		},
		"path": config.SettingsPath(),
	})
}

// settingsBody is the save payload. Pointers distinguish "not sent" from
// "sent empty", which is what lets one field be saved without clearing others.
type settingsBody struct {
	BaseURL          *string                    `json:"base_url"`
	APIKey           *string                    `json:"api_key"`
	Models           *[]string                  `json:"models"`
	MaxTokens        *int                       `json:"max_tokens"`
	Reasoning        *map[string]any            `json:"reasoning"`
	ReasoningByModel *map[string]map[string]any `json:"reasoning_by_model"`
}

// merge folds the payload into the stored settings.
func (b settingsBody) merge(into config.SettingsLLM) config.SettingsLLM {
	if b.BaseURL != nil {
		into.BaseURL = strings.TrimSpace(*b.BaseURL)
	}
	// An omitted or blank key keeps the stored one. There is deliberately no way
	// to clear it by accident; clearing means sending a single space, which the
	// UI does not do.
	if b.APIKey != nil && strings.TrimSpace(*b.APIKey) != "" {
		into.APIKey = strings.TrimSpace(*b.APIKey)
	}
	if b.Models != nil {
		var out []string
		for _, m := range *b.Models {
			if m = strings.TrimSpace(m); m != "" {
				out = append(out, m)
			}
		}
		into.Models = out
	}
	if b.MaxTokens != nil {
		into.MaxTokens = *b.MaxTokens
	}
	if b.Reasoning != nil {
		into.Reasoning = *b.Reasoning
	}
	if b.ReasoningByModel != nil {
		into.ReasoningByModel = *b.ReasoningByModel
	}
	return into
}

// SaveSettings persists the settings and applies them to the running process.
func (a *API) SaveSettings(_ context.Context, c *app.RequestContext) {
	var body settingsBody
	if err := bind(c, &body); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	path := config.SettingsPath()
	saved, err := config.LoadSettings(path)
	if err != nil {
		fail(c, consts.StatusInternalServerError, "读取设置失败: "+err.Error())
		return
	}
	saved.LLM = body.merge(saved.LLM)
	if err := config.SaveSettings(path, saved); err != nil {
		a.Log.Error("write settings", "err", err)
		fail(c, consts.StatusInternalServerError, "保存设置失败: "+err.Error())
		return
	}
	// Persist first, then apply. A save that took effect but was not written
	// would vanish on restart with no sign it had ever worked.
	eff := a.Chat.ApplySettings(saved.LLM)
	redacted, hasKey := saved.LLM.Redacted()
	ok(c, map[string]any{
		"saved":   redacted,
		"has_key": hasKey,
		"effective": map[string]any{
			"base_url":   eff.OpenAIBaseURL,
			"models":     eff.SelectableModels(),
			"max_tokens": eff.MaxTokens,
		},
	})
}

// TestSettings checks an endpoint before it is saved, so a typo cannot leave
// the deployment unable to reach any model. Uses the submitted values, falling
// back to what is already stored — including the key, so a URL can be tested
// without re-typing it.
func (a *API) TestSettings(ctx context.Context, c *app.RequestContext) {
	var body settingsBody
	if err := bind(c, &body); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	saved, _ := config.LoadSettings(config.SettingsPath())
	merged := body.merge(saved.LLM).Apply(a.Chat.EffectiveLLM())

	model := merged.DefaultModel()
	if model == "" {
		fail(c, consts.StatusBadRequest, "没有可用模型:先填一个模型名")
		return
	}
	client := llm.NewOpenAIClientCapped(merged.OpenAIBaseURL, merged.OpenAIAPIKey, 32)
	client.Reasoning, client.ReasoningByModel = merged.Reasoning, merged.ReasoningByModel
	resp, err := client.Chat(llm.WithAgent(ctx, "settings-test"), llm.Request{
		Model:     model,
		MaxTokens: 32,
		Messages:  []llm.ChatMessage{{Role: llm.RoleUser, Content: "ping"}},
	})
	if err != nil {
		// Reported as a RESULT, not an error status: a failed probe is the answer
		// the user asked for, and the panel should show why rather than a toast.
		ok(c, map[string]any{"ok": false, "model": model, "error": err.Error()})
		return
	}
	ok(c, map[string]any{
		"ok": true, "model": model,
		"reply":  strings.TrimSpace(resp.Content),
		"tokens": resp.Usage.TotalTokens,
	})
}
