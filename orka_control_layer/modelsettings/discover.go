package modelsettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// DiscoverResult identifies whether models were returned by the configured
// provider or are unverified preset candidates for a known subscription endpoint.
type DiscoverResult struct {
	Models []string `json:"models"`
	Source string   `json:"source"`
	Notice string   `json:"notice"`
}

// Discover preserves the list-only interface for existing callers. Callers that
// display discovery results should use DiscoverWithMetadata to disclose presets.
func (s *Store) Discover(ctx context.Context, owner, base, key string) ([]string, error) {
	result, err := s.DiscoverWithMetadata(ctx, owner, base, key)
	return result.Models, err
}

// DiscoverWithMetadata never follows redirects or returns provider error bodies.
// Localhost and private-network endpoints remain supported for local providers.
// Presets do not validate credentials or change the configured base URL.
func (s *Store) DiscoverWithMetadata(ctx context.Context, owner, base, key string) (DiscoverResult, error) {
	return s.discover(ctx, owner, base, key, true)
}

func (s *Store) discover(ctx context.Context, owner, base, key string, useActive bool) (DiscoverResult, error) {
	base, err := NormalizeURL(base)
	if err != nil {
		return DiscoverResult{}, err
	}
	if strings.TrimSpace(owner) == "" {
		return DiscoverResult{}, ErrOwner
	}
	key = strings.TrimSpace(key)
	if !validText(key, 8192) {
		return DiscoverResult{}, errors.New("invalid api_key")
	}
	if useActive {
		saved, _, err := s.Get(owner)
		if err != nil {
			return DiscoverResult{}, err
		}
		if saved.BaseURL == base {
			if err := RequireSupportedProtocol(saved.Protocol); err != nil {
				return DiscoverResult{}, err
			}
			if key == "" {
				key = saved.APIKey
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return DiscoverResult{}, errors.New("invalid base_url")
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return DiscoverResult{}, errors.New("model discovery failed or timed out")
	}
	defer resp.Body.Close()
	if err := ctx.Err(); err != nil {
		return DiscoverResult{}, errors.New("model discovery failed or timed out")
	}
	if resp.StatusCode/100 != 2 {
		if (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) && arkPresetEndpoint(base) {
			return DiscoverResult{
				Models: arkPresetModels(),
				Source: "preset",
				Notice: "此服务未提供模型列表，已载入火山方舟预设候选；列表不代表密钥或套餐权限已验证，可手动调整。ark-code-latest 使用方舟控制台所选模型。",
			}, nil
		}
		return DiscoverResult{}, fmt.Errorf("model discovery returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return DiscoverResult{}, errors.New("model discovery response could not be read")
	}
	if len(b) > maxResponseBytes {
		return DiscoverResult{}, errors.New("model discovery response exceeds 1 MiB")
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &body) != nil || body.Data == nil {
		return DiscoverResult{}, errors.New("invalid model discovery response")
	}
	models := make([]string, 0, len(body.Data))
	seen := map[string]bool{}
	for _, item := range body.Data {
		m := strings.TrimSpace(item.ID)
		if m != "" && validText(m, 256) && !seen[m] {
			models = append(models, m)
			seen[m] = true
		}
	}
	if len(models) > 2048 {
		return DiscoverResult{}, errors.New("too many discovered models")
	}
	sort.Strings(models)
	return DiscoverResult{Models: models, Source: "remote"}, nil
}

// Compare the normalized URL literally: reject alternate hosts, ports, escaped
// paths and lookalike prefixes. Never probe or rewrite to another billing route.
func arkPresetEndpoint(base string) bool {
	switch base {
	case "https://ark.cn-beijing.volces.com/api/plan/v3", "https://ark.cn-beijing.volces.com/api/coding/v3":
		return true
	default:
		return false
	}
}

// Catalog checked against official documentation on 2026-09-14:
// Coding Plan (updated 2026-09-08): https://www.volcengine.com/docs/82379/1928261
// Agent Plan (updated 2026-09-14): https://www.volcengine.com/docs/82379/2366394
// Console-selected ark-code-latest alias: https://www.volcengine.com/docs/82379/2373738
// These are candidates, not a per-account entitlement or credential check.
func arkPresetModels() []string {
	return []string{
		"doubao-seed-2.1-turbo",
		"doubao-seed-evolving",
		"doubao-seed-2.0-lite",
		"minimax-m3",
		"glm-5.3",
		"glm-latest",
		"glm-5.3-flash",
		"deepseek-v4-flash",
		"deepseek-v4-pro",
		"kimi-k2.7-code",
		"kimi-k3",
		"ark-code-latest",
	}
}

// DiscoverProfile scopes a blank credential to the named connection, even when
// two accounts at the same provider share a URL. An unsaved ID uses only its
// explicitly supplied key; it can never inherit another connection's key.
func (s *Store) DiscoverProfile(ctx context.Context, owner, id, base, key, protocol string) (DiscoverResult, error) {
	if protocol == "" {
		protocol = "openai-compatible"
	}
	if err := RequireSupportedProtocol(protocol); err != nil {
		return DiscoverResult{}, err
	}
	base, err := NormalizeURL(base)
	if err != nil {
		return DiscoverResult{}, err
	}
	p, err := s.GetProfiles(owner)
	if err != nil {
		return DiscoverResult{}, err
	}
	c, found := findProfile(p, id)
	if strings.TrimSpace(key) == "" && found && c.BaseURL == base && c.Protocol == protocol {
		key = c.APIKey
	}
	return s.discover(ctx, owner, base, key, false)
}
