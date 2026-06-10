package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_core/security"
	"github.com/orka-oss/orka_middleware/local/filesystem"
	mcpclient "github.com/orka-oss/orka_middleware/mcp"
	"github.com/orka-oss/orka_middleware/toolsmanager"
)

// GUITool is the GUI automation tool injected by the providers. It defaults to
// a mock; main wires the real run_agent (WebSocket) tool when GUI_AGENT_WS_URL
// is configured.
var GUITool agent.BaseTool = guiMockTool{}

// LocalToolsProvider serves the real local filesystem tools (confined to the
// per-user storage root) plus the GUI tool. No remote dependency.
func LocalToolsProvider(baseStorage string) ToolsProvider {
	return func(_ context.Context, req ChatRunRequest) ([]agent.BaseTool, func(), error) {
		root := pathsafe.UserRoot(baseStorage, req.UserEmail)
		tools := append(filesystem.New(root), GUITool)
		return filterEnabled(tools, req.EnabledTools), nil, nil
	}
}

// mcpPool reuses one MCP connection per user across chat requests instead of
// dialing + handshaking on every message. Entries are keyed by user email
// because the signed context token (and thus the gateway's per-user identity +
// RBAC) is baked into the client's headers. Entries are refreshed before the
// token expires so the gateway never sees a stale token.
type mcpPool struct {
	baseStorage string
	mcpURL      string
	secret      string
	tokenTTL    time.Duration
	maxAge      time.Duration // refresh entries older than this (< tokenTTL)
	scopes      []string

	mu      sync.Mutex
	entries map[string]*mcpEntry
}

type mcpEntry struct {
	client   *mcpclient.Client
	tools    []agent.BaseTool // gateway tools bound to this user's identity
	created  time.Time
	lastUsed time.Time
}

// get returns a live (client, tools) for the user, creating/refreshing as
// needed. The pool owns the client lifecycle; callers must not close it.
func (p *mcpPool) get(ctx context.Context, email string) ([]agent.BaseTool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if e := p.entries[email]; e != nil && time.Since(e.created) < p.maxAge {
		e.lastUsed = time.Now()
		return e.tools, nil
	}
	if e := p.entries[email]; e != nil { // stale → close and rebuild
		_ = e.client.Close()
		delete(p.entries, email)
	}

	tok, err := security.Sign(security.NewToken(email, p.scopes, p.tokenTTL), []byte(p.secret))
	if err != nil {
		return nil, err
	}
	client, err := mcpclient.New(ctx, mcpclient.Config{
		Transport: mcpclient.TransportStreamableHTTP,
		URL:       p.mcpURL,
		Headers:   map[string]string{"X-Orka-Token": tok},
		Name:      "orka-control",
	})
	if err != nil {
		return nil, err
	}
	// Only the GUI tool is local; file_*/web_* come from the gateway over MCP.
	tm := toolsmanager.New([]agent.BaseTool{GUITool}, client)
	tools, err := tm.GetTools(ctx)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	now := time.Now()
	p.entries[email] = &mcpEntry{client: client, tools: tools, created: now, lastUsed: now}
	return tools, nil
}

// janitor periodically closes connections that have been idle longer than
// maxAge, releasing their sockets/fds instead of leaking for offline users.
func (p *mcpPool) janitor(ctx context.Context) {
	t := time.NewTicker(p.maxAge)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			p.closeAll()
			return
		case <-t.C:
			p.mu.Lock()
			for email, e := range p.entries {
				if time.Since(e.lastUsed) >= p.maxAge {
					_ = e.client.Close()
					delete(p.entries, email)
				}
			}
			p.mu.Unlock()
		}
	}
}

func (p *mcpPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for email, e := range p.entries {
		_ = e.client.Close()
		delete(p.entries, email)
	}
}

// MCPToolsProviderPooled is MCPToolsProvider with a per-user connection pool.
// On any pool error it degrades to local filesystem tools (+ GUI).
func MCPToolsProviderPooled(baseStorage, mcpURL, secret string, tokenTTL time.Duration, scopes []string) ToolsProvider {
	maxAge := tokenTTL - time.Minute
	if maxAge < 30*time.Second {
		maxAge = tokenTTL / 2
	}
	pool := &mcpPool{
		baseStorage: baseStorage, mcpURL: mcpURL, secret: secret,
		tokenTTL: tokenTTL, maxAge: maxAge, scopes: scopes,
		entries: map[string]*mcpEntry{},
	}
	go pool.janitor(context.Background()) // evict idle connections for process lifetime
	return func(ctx context.Context, req ChatRunRequest) ([]agent.BaseTool, func(), error) {
		tools, err := pool.get(ctx, req.UserEmail)
		if err != nil {
			root := pathsafe.UserRoot(baseStorage, req.UserEmail)
			fallback := append(filesystem.New(root), GUITool)
			return filterEnabled(fallback, req.EnabledTools), nil, err
		}
		// no cleanup: the pool owns the connection lifecycle.
		return filterEnabled(tools, req.EnabledTools), nil, nil
	}
}

// Grouper is an optional capability: a tool that reports its own UI group. When
// a tool implements it, filterEnabled trusts that over the name heuristic — the
// path for future tools (and the eventual gateway-reported group) to be exact.
type Grouper interface{ Group() string }

// toolGroup returns a tool's group, preferring a self-reported Group() and
// falling back to a name-convention heuristic that mirrors the gateway Registry.
func toolGroup(t agent.BaseTool) string {
	if g, ok := t.(Grouper); ok {
		if grp := g.Group(); grp != "" {
			return grp
		}
	}
	return groupForName(t.Name())
}

func groupForName(name string) string {
	switch {
	case strings.HasPrefix(name, "file_"):
		return "file"
	case name == "run_agent":
		return "gui_agent"
	case name == "web_search" || name == "fetch_url" || name == "weather" || name == "http_request":
		return "web"
	case name == "current_time" || name == "calculator" || name == "unit_convert":
		return "util"
	}
	return ""
}

// filterEnabled keeps the tools matching the request's enabled set, by exact
// name or by group. An empty set means "all". "search" is a back-compat alias
// for the "web" group.
func filterEnabled(tools []agent.BaseTool, enabled []string) []agent.BaseTool {
	if len(enabled) == 0 {
		return tools
	}
	want := map[string]bool{}
	for _, e := range enabled {
		want[e] = true
	}
	var out []agent.BaseTool
	for _, t := range tools {
		grp := toolGroup(t)
		if want[t.Name()] || want[grp] || (want["search"] && grp == "web") {
			out = append(out, t)
		}
	}
	return out
}
