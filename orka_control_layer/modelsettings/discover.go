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

// Discover never follows redirects or returns provider error bodies. Localhost
// and private-network endpoints are intentionally supported for local providers.
func (s *Store) Discover(ctx context.Context, owner, base, key string) ([]string, error) {
	base, err := NormalizeURL(base)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(owner) == "" {
		return nil, ErrOwner
	}
	key = strings.TrimSpace(key)
	if !validText(key, 8192) {
		return nil, errors.New("invalid api_key")
	}
	if key == "" {
		saved, _, err := s.Get(owner)
		if err != nil {
			return nil, err
		}
		if saved.BaseURL == base {
			key = saved.APIKey
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, errors.New("invalid base_url")
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("model discovery failed or timed out")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("model discovery returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, errors.New("model discovery response could not be read")
	}
	if len(b) > maxResponseBytes {
		return nil, errors.New("model discovery response exceeds 1 MiB")
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &body) != nil || body.Data == nil {
		return nil, errors.New("invalid model discovery response")
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
		return nil, errors.New("too many discovered models")
	}
	sort.Strings(models)
	return models, nil
}
