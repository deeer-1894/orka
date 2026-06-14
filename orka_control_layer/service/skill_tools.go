package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
)

// find_skills + skill_create round out the Claude-Code-style skill system: the
// agent can discover existing skills and author new ones at runtime, on top of
// apply_skill (adopt) and the filesystem-loaded catalog (LoadSkills). They are
// always available (added after the tool-group filter) like apply_skill.

type findSkillsTool struct{}

func (findSkillsTool) Name() string { return "find_skills" }
func (findSkillsTool) Description() string {
	return "Search available skills by keyword. Returns matching skill names + descriptions; adopt one with apply_skill. Call with an empty query to list all."
}
func (findSkillsTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "keywords to match against skill name/description (empty = list all)"},
		},
	}
}
func (findSkillsTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	q := strings.ToLower(strings.TrimSpace(asStr(args["query"])))
	var b strings.Builder
	n := 0
	for _, s := range middlewares.AllSkills() {
		if q == "" || strings.Contains(strings.ToLower(s.Name), q) || strings.Contains(strings.ToLower(s.Desc), q) {
			fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Desc)
			n++
		}
	}
	if n == 0 {
		return fmt.Sprintf("No skills match %q. Use skill_create to author one, or call find_skills with an empty query to list all.", q), nil
	}
	return fmt.Sprintf("Found %d skill(s) — adopt with apply_skill:\n%s", n, b.String()), nil
}

type skillCreateTool struct{}

func (skillCreateTool) Name() string { return "skill_create" }
func (skillCreateTool) Description() string {
	return "Create a reusable skill (an expertise prompt-pack) that persists for future tasks. Use when you've crafted guidance worth saving. Args: name (kebab/snake), description (when to use it), instructions (the expert guidance)."
}
func (skillCreateTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":         map[string]any{"type": "string", "description": "short id, letters/digits/_/- only"},
			"description":  map[string]any{"type": "string", "description": "one line: when this skill should be used"},
			"instructions": map[string]any{"type": "string", "description": "the expert guidance / methodology (the SKILL.md body)"},
		},
		"required": []string{"name", "description", "instructions"},
	}
}
func (skillCreateTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	def := middlewares.SkillDef{
		Name:   asStr(args["name"]),
		Desc:   asStr(args["description"]),
		Prompt: asStr(args["instructions"]),
	}
	if strings.TrimSpace(def.Desc) == "" || strings.TrimSpace(def.Prompt) == "" {
		return "", fmt.Errorf("skill_create needs a non-empty description and instructions")
	}
	if err := middlewares.RegisterSkill(def, true); err != nil {
		return "", err
	}
	return fmt.Sprintf("Created skill %q. It's now adoptable with apply_skill and will survive a restart.", strings.ToLower(strings.TrimSpace(def.Name))), nil
}

type skillInstallTool struct{}

func (skillInstallTool) Name() string { return "skill_install" }
func (skillInstallTool) Description() string {
	return "Install a skill from the web: download a SKILL.md (Claude-Code-style: name + description frontmatter and a markdown body) from a URL and register it so it's adoptable with apply_skill and survives restart. Args: url (a raw SKILL.md URL, e.g. a GitHub raw link)."
}
func (skillInstallTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string", "description": "URL of a raw SKILL.md file"},
		},
		"required": []string{"url"},
	}
}
func (skillInstallTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	url := strings.TrimSpace(asStr(args["url"]))
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("url must be an http(s) link to a raw SKILL.md")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024)) // cap at 256KB
	if err != nil {
		return "", err
	}
	name, err := middlewares.InstallSkillMD(string(body))
	if err != nil {
		return "", fmt.Errorf("invalid SKILL.md: %w", err)
	}
	return fmt.Sprintf("Installed skill %q from %s. It's now adoptable with apply_skill.", name, url), nil
}

// SkillTools returns the always-available skill-management tools.
func SkillTools() []agent.BaseTool {
	return []agent.BaseTool{findSkillsTool{}, skillCreateTool{}, skillInstallTool{}}
}

func asStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
