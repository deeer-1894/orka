package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_control_layer/scheduled_task"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
)

// ListSkills returns the live skill catalog (builtin + filesystem + installed),
// name + description only — so the UI can show every adoptable skill.
func (a *API) ListSkills(ctx context.Context, c *app.RequestContext) {
	defs, err := middlewares.VisibleSkills(middlewares.WithSkillOwner(ctx, a.BaseStorage, authEmail(c)))
	if err != nil {
		fail(c, 500, "skill storage unavailable")
		return
	}
	scoped := middlewares.WithSkillOwner(ctx, a.BaseStorage, authEmail(c))
	off := middlewares.DisabledSkills(scoped)
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		_, builtin := middlewares.GetSkill(d.Name)
		out = append(out, map[string]any{"name": d.Name, "description": d.Desc, "enabled": !off[d.Name], "builtin": builtin})
	}
	ok(c, map[string]any{"skills": out})
}

// GetSkill returns one skill's full content (name, description, prompt body) so
// the UI can preview what a skill actually does before adopting it.
func (a *API) GetSkill(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Name string `json:"name"`
	}
	if err := bind(c, &req); err != nil || req.Name == "" {
		fail(c, consts.StatusBadRequest, "name required")
		return
	}
	d, found, err := middlewares.GetVisibleSkill(middlewares.WithSkillOwner(ctx, a.BaseStorage, authEmail(c)), req.Name)
	if err != nil {
		fail(c, 500, "skill storage unavailable")
		return
	}
	if !found {
		fail(c, consts.StatusNotFound, "skill not found")
		return
	}
	ok(c, map[string]string{"name": d.Name, "description": d.Desc, "prompt": d.Prompt})
}

// DeleteSkill removes a non-builtin skill.
func (a *API) DeleteSkill(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Name string `json:"name"`
	}
	if err := bind(c, &req); err != nil || req.Name == "" {
		fail(c, consts.StatusBadRequest, "name required")
		return
	}
	if err := middlewares.DeletePersonalSkill(middlewares.WithSkillOwner(ctx, a.BaseStorage, authEmail(c)), req.Name); err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	ok(c, map[string]string{"status": "deleted"})
}

// fetchSkillMD downloads a raw SKILL.md. The address is user input fetched
// from inside the deployment, so the client refuses private and loopback
// targets on every dial.
func fetchSkillMD(ctx context.Context, raw string) (string, error) {
	link := strings.TrimSpace(raw)
	if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
		return "", errors.New("请输入 SKILL.md 原文的 http(s) 链接")
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", errors.New("链接无效")
	}
	resp, err := scheduled_task.WatchClient().Do(hreq)
	if err != nil {
		return "", errors.New("无法下载：" + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", errors.New("无法下载：HTTP " + strings.TrimSpace(resp.Status))
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	return string(body), nil
}

// PreviewSkill downloads and parses a SKILL.md without installing it, so the
// user sees what a skill will tell the model before adding it.
func (a *API) PreviewSkill(ctx context.Context, c *app.RequestContext) {
	var req struct {
		URL string `json:"url"`
	}
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request")
		return
	}
	body, err := fetchSkillMD(ctx, req.URL)
	if err != nil {
		fail(c, consts.StatusBadGateway, err.Error())
		return
	}
	def, err := middlewares.ParseSkillMD(body)
	if err != nil {
		fail(c, consts.StatusBadRequest, "不是有效的 SKILL.md："+err.Error())
		return
	}
	ok(c, map[string]string{"name": def.Name, "description": def.Desc, "prompt": def.Prompt})
}

// InstallSkill downloads a SKILL.md from a URL and registers it (persisted).
func (a *API) InstallSkill(ctx context.Context, c *app.RequestContext) {
	var req struct {
		URL string `json:"url"`
	}
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	body, err := fetchSkillMD(ctx, req.URL)
	if err != nil {
		fail(c, consts.StatusBadGateway, err.Error())
		return
	}
	name, err := middlewares.InstallPersonalSkill(middlewares.WithSkillOwner(ctx, a.BaseStorage, authEmail(c)), body)
	if err != nil {
		fail(c, consts.StatusBadRequest, "不是有效的 SKILL.md："+err.Error())
		return
	}
	ok(c, map[string]string{"name": name})
}

// EnableSkill switches one of the caller's visible skills on or off.
func (a *API) EnableSkill(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := bind(c, &req); err != nil || req.Name == "" {
		fail(c, consts.StatusBadRequest, "name required")
		return
	}
	if err := middlewares.SetSkillEnabled(middlewares.WithSkillOwner(ctx, a.BaseStorage, authEmail(c)), req.Name, req.Enabled); err != nil {
		fail(c, consts.StatusNotFound, "skill not found")
		return
	}
	ok(c, map[string]any{"name": req.Name, "enabled": req.Enabled})
}
