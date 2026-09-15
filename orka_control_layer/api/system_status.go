package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/redis/go-redis/v9"
)

// BuildVersion and BuildTime may be set by -ldflags; development falls back to
// Go's VCS build metadata. They never read or expose the deployment environment.
var BuildVersion = "dev"
var BuildTime = ""
var statusStartedAt = time.Now().UTC()

type ServiceReadiness struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type readinessCheck struct {
	name       string
	configured bool
	probe      func(context.Context) error
}

// SystemStatus is an authenticated readiness snapshot. All checks are bounded
// and read-only protocol checks; they never submit a model or browser task.
// Register alongside authenticated controller routes as GET /system/status.
func (a *API) SystemStatus(ctx context.Context, c *app.RequestContext) {
	if authEmail(c) == "" {
		fail(c, consts.StatusUnauthorized, "authentication required")
		return
	}
	redisAddr, guiURL := "", ""
	if a.Chat != nil && a.Chat.Cfg != nil {
		redisAddr = a.Chat.Cfg.Storage.RedisAddr
		guiURL = a.Chat.Cfg.Agent.GUIAgentWSURL
	}
	toolsURL := os.Getenv("TOOLS_MCP_URL")
	checks := []readinessCheck{
		{name: "mongo", configured: a.Store != nil, probe: func(ctx context.Context) error { return a.Store.Ping(ctx) }},
		{name: "redis", configured: redisAddr != "", probe: func(ctx context.Context) error {
			client := redis.NewClient(&redis.Options{Addr: redisAddr, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, MaxRetries: -1})
			defer client.Close()
			return client.Ping(ctx).Err()
		}},
		{name: "tools", configured: toolsURL != "", probe: func(ctx context.Context) error { return probeTools(ctx, toolsURL) }},
		{name: "gui", configured: guiURL != "", probe: func(ctx context.Context) error {
			if os.Getenv("GUI_AUTH_TOKEN") == "" {
				return errors.New("GUI authentication not configured")
			}
			return probeGUI(ctx, guiURL)
		}},
	}
	services := collectReadiness(ctx, checks)
	ready := true
	for i, service := range services {
		if service.Status != "ready" && (i < 2 || checks[i].configured) {
			ready = false
		}
		if service.Status == "ready" && service.Name == "tools" {
			services[i].Detail = "MCP initialization reachable; tool calls not executed"
		}
		if service.Status == "ready" && service.Name == "gui" {
			services[i].Detail = "health endpoint reachable; browser and model not executed"
		}
	}
	version := BuildVersion
	modified := false
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if version == "dev" {
					version = setting.Value
				}
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
	}
	ok(c, map[string]any{"version": version, "build_time": BuildTime, "modified": modified, "started_at": statusStartedAt, "ready": ready, "services": services, "model_probe": "not_run"})
}

func collectReadiness(ctx context.Context, checks []readinessCheck) []ServiceReadiness {
	out := make([]ServiceReadiness, len(checks))
	var wg sync.WaitGroup
	for i, check := range checks {
		wg.Add(1)
		go func(i int, check readinessCheck) {
			defer wg.Done()
			r := ServiceReadiness{Name: check.name, Status: "not_configured", Detail: "not configured"}
			if check.configured {
				probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				r.Status = "ready"
				r.Detail = "connection available"
				if check.probe == nil || check.probe(probeCtx) != nil {
					r.Status = "unavailable"
					r.Detail = "readiness check failed; verify configuration and connectivity"
				}
			}
			out[i] = r
		}(i, check)
	}
	wg.Wait()
	return out
}

func statusHTTPClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func probeGUI(ctx context.Context, endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return errors.New("invalid GUI URL")
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return errors.New("invalid GUI protocol")
	}
	u.Path = "/health"
	u.RawQuery = ""
	u.Fragment = ""
	u.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := statusHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("GUI health unavailable")
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&body); err != nil {
		return err
	}
	if body.Status != "ok" {
		return errors.New("GUI is not healthy")
	}
	return nil
}

func probeTools(ctx context.Context, endpoint string) error {
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"orka-readiness","version":"1"}}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(request))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	client := statusHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("MCP initialization unavailable")
	}
	// Dispose of a server session allocated by initialization, if it has one.
	if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
		defer func() {
			cleanup, c := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer c()
			r, e := http.NewRequestWithContext(cleanup, http.MethodDelete, endpoint, nil)
			if e != nil {
				return
			}
			r.Header.Set("Mcp-Session-Id", session)
			if reply, e := client.Do(r); e == nil {
				reply.Body.Close()
			}
		}()
	}
	var payload []byte
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, 64<<10))
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "data:") {
				payload = []byte(strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "data:")))
				break
			}
		}
	} else {
		payload, err = io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if err != nil {
			return err
		}
	}
	var reply struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			ProtocolVersion string                     `json:"protocolVersion"`
			Capabilities    map[string]json.RawMessage `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &reply); err != nil {
		return err
	}
	if len(reply.Error) > 0 || reply.Result.ProtocolVersion == "" || reply.Result.Capabilities == nil {
		return errors.New("invalid MCP initialization response")
	}
	return nil
}
