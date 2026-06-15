package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_control_layer/service/middlewares"
)

// ListSkills returns the live skill catalog (builtin + filesystem + installed),
// name + description only — so the UI can show every adoptable skill.
func (a *API) ListSkills(ctx context.Context, c *app.RequestContext) {
	defs := middlewares.AllSkills()
	out := make([]map[string]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, map[string]string{"name": d.Name, "description": d.Desc})
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
	d, found := middlewares.GetSkill(req.Name)
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
	if err := middlewares.DeleteSkill(req.Name); err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	ok(c, map[string]string{"status": "deleted"})
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
	url := strings.TrimSpace(req.URL)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		fail(c, consts.StatusBadRequest, "url must be an http(s) link to a raw SKILL.md")
		return
	}
	hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(hreq)
	if err != nil {
		fail(c, consts.StatusBadGateway, "download failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		fail(c, consts.StatusBadGateway, "download failed: HTTP "+strings.TrimSpace(resp.Status))
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	name, err := middlewares.InstallSkillMD(string(body))
	if err != nil {
		fail(c, consts.StatusBadRequest, "invalid SKILL.md: "+err.Error())
		return
	}
	ok(c, map[string]string{"name": name})
}
