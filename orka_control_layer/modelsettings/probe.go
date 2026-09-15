package modelsettings

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

var ErrProtocolUnsupported = errors.New("native protocol is not implemented; use an explicit OpenAI-compatible connection")
var probeProviderTransport = llm.NewOpenAIClient("", "").HTTP.Transport

var ErrProfileChanged = errors.New("profile changed during probe; run the probe again")

func RequireSupportedProtocol(protocol string) error {
	if protocol != "" && protocol != "openai-compatible" {
		return ErrProtocolUnsupported
	}
	return nil
}
func findProfile(p Profiles, id string) (Config, bool) {
	for _, c := range p.Profiles {
		if c.ID == id {
			return c, true
		}
	}
	return Config{}, false
}
func containsModel(c Config, model string) bool {
	for _, m := range c.Models {
		if m == model {
			return true
		}
	}
	return false
}

// Probe checks only explicitly requested capabilities, never lists as proof of
// access. It performs no tools or user actions and does not retry billed calls.
// Network work runs outside the store lock; stale evidence cannot overwrite a
// rotated credential/endpoint. The caller supplies its normal usage/budget ctx.
func (s *Store) Probe(ctx context.Context, owner, id, model string, capabilities []string) (Verification, error) {
	if len(capabilities) == 0 || len(capabilities) > 3 {
		return Verification{}, errors.New("select text, vision or tools to probe")
	}
	seen := map[string]bool{}
	for _, kind := range capabilities {
		if kind != "text" && kind != "vision" && kind != "tools" || seen[kind] {
			return Verification{}, errors.New("invalid probe capabilities")
		}
		seen[kind] = true
	}
	p, err := s.GetProfiles(owner)
	if err != nil {
		return Verification{}, err
	}
	c, ok := findProfile(p, id)
	if !ok || !containsModel(c, model) {
		return Verification{}, errors.New("profile or configured model not found")
	}
	if err = RequireSupportedProtocol(c.Protocol); err != nil {
		return Verification{}, err
	}
	client := llm.NewOpenAIClient(c.BaseURL, c.APIKey)
	client.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// Probe bodies have a small fixed output budget and a bounded response reader.
	client.HTTP.Transport = &probeTransport{base: probeProviderTransport}
	accounted := llm.NewAccounted(probeClient{Client: client})
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	checks := map[string]Check{}
	for _, kind := range capabilities {
		if err = ctx.Err(); err != nil {
			return Verification{}, err
		}
		req, accept, err := probeRequest(model, kind)
		if err != nil {
			return Verification{}, errors.New("could not create probe challenge")
		}
		// Probe has its own small output ceiling, but preserves the explicitly
		// configured reasoning effort. Set both before admission so accounting
		// reserves the same maximum that is sent to the provider.
		req.ReasoningEffort = c.Policies[model].ReasoningEffort
		if req.ReasoningEffort != "" && req.ReasoningEffort != "none" {
			req.MaxTokens = 2048
		}
		check := Check{CheckedAt: time.Now().UTC()}
		resp, callErr := accounted.Chat(ctx, req)
		if llm.IsCallLimit(callErr) {
			return Verification{}, callErr
		}
		check.Verified = callErr == nil && accept(resp)
		if !check.Verified {
			var apiErr *llm.APIError
			switch {
			case errors.As(callErr, &apiErr):
				check.Error = fmt.Sprintf("provider returned HTTP %d", apiErr.Status)
			case callErr != nil:
				check.Error = "probe failed or timed out"
			default:
				check.Error = "provider did not satisfy the capability challenge"
			}
		}
		checks[kind] = check
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, exists, err := s.read(owner)
	if err != nil {
		return Verification{}, err
	}
	latest, err := profilesFromDisk(d, exists)
	if err != nil {
		return Verification{}, err
	}
	current, ok := findProfile(latest, id)
	if !ok || current.BaseURL != c.BaseURL || current.Protocol != c.Protocol || current.APIKey != c.APIKey || !containsModel(current, model) {
		return Verification{}, ErrProfileChanged
	}
	v := current.Verified[model]
	for kind, check := range checks {
		dst := &v.Text
		if kind == "vision" {
			dst = &v.Vision
		}
		if kind == "tools" {
			dst = &v.Tools
		}
		if !dst.CheckedAt.After(check.CheckedAt) {
			*dst = check
		}
	}
	for i := range latest.Profiles {
		if latest.Profiles[i].ID == id {
			if latest.Profiles[i].Verified == nil {
				latest.Profiles[i].Verified = map[string]Verification{}
			}
			latest.Profiles[i].Verified[model] = v
		}
	}
	if err = s.write(owner, profilesToDisk(latest)); err != nil {
		return Verification{}, err
	}
	return v, nil
}

func probeRequest(model, kind string) (llm.Request, func(llm.Response) bool, error) {
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return llm.Request{}, nil, err
	}
	challenge := "ORKA_OK_" + hex.EncodeToString(nonce)
	req := llm.Request{Model: model, MaxTokens: 128, Messages: []llm.ChatMessage{{Role: llm.RoleUser, Content: "Reply exactly with: " + challenge}}}
	accept := func(r llm.Response) bool { return strings.TrimSpace(r.Content) == challenge }
	switch kind {
	case "vision":
		palette := []struct {
			name string
			rgb  color.RGBA
		}{{"red", color.RGBA{255, 0, 0, 255}}, {"green", color.RGBA{0, 255, 0, 255}}, {"blue", color.RGBA{0, 0, 255, 255}}, {"yellow", color.RGBA{255, 255, 0, 255}}}
		// Eight independent cells make a blind complete guess 1 in 4^8,
		// while keeping one small image and one provider exchange.
		const cells, cellSize = 8, 32
		colors := make([]string, cells)
		img := image.NewRGBA(image.Rect(0, 0, cells*cellSize, cellSize))
		for i := range colors {
			chosen := palette[int(nonce[i])%len(palette)]
			colors[i] = chosen.name
			for y := 0; y < cellSize; y++ {
				for x := 0; x < cellSize; x++ {
					pixel := chosen.rgb
					if x == 0 || y == 0 || x == cellSize-1 || y == cellSize-1 {
						pixel = color.RGBA{255, 255, 255, 255}
					}
					img.SetRGBA(i*cellSize+x, y, pixel)
				}
			}
		}
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			return llm.Request{}, nil, err
		}
		req.Messages[0].Content = "Read the eight equal colored cells in this image from left to right. Ignore the white borders. Reply with all eight lowercase English color names separated by commas, with no spaces or other text. Each cell is red, green, blue, or yellow; repeated colors must be repeated in your answer."
		req.Messages[0].Images = []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())}
		accept = func(r llm.Response) bool {
			return strings.ToLower(strings.TrimSpace(r.Content)) == strings.Join(colors, ",")
		}
	case "tools":
		req.Messages[0].Content = "Call report_probe with token set to " + challenge + ". Do not answer with text."
		req.Tools = []llm.ToolSpec{{Name: "report_probe", Description: "A no-op connection probe; no action is executed", Parameters: map[string]any{"type": "object", "properties": map[string]any{"token": map[string]any{"type": "string"}}, "required": []string{"token"}, "additionalProperties": false}}}
		accept = func(r llm.Response) bool {
			if len(r.ToolCalls) != 1 || r.ToolCalls[0].Name != "report_probe" || r.ToolCalls[0].ID == "" {
				return false
			}
			var args struct {
				Token string `json:"token"`
			}
			return json.Unmarshal([]byte(r.ToolCalls[0].Arguments), &args) == nil && args.Token == challenge
		}
	}
	return req, accept, nil
}

// Provider bodies may echo credentials. Strip them before accounting as well
// as before public conversion so nested settlement failures cannot retain keys.
type probeClient struct{ llm.Client }

func (c probeClient) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	resp, err := c.Client.Chat(ctx, req)
	if err == nil {
		return resp, nil
	}
	if errors.Is(err, context.Canceled) {
		return resp, context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return resp, context.DeadlineExceeded
	}
	var apiErr *llm.APIError
	if errors.As(err, &apiErr) {
		return resp, &llm.APIError{Status: apiErr.Status, Body: "provider request failed"}
	}
	return resp, errors.New("probe provider request failed")
}
