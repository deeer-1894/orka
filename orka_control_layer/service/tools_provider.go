package service

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_core/security"
	"github.com/orka-oss/orka_middleware/local/filesystem"
	mcpclient "github.com/orka-oss/orka_middleware/mcp"
	"github.com/orka-oss/orka_middleware/toolsmanager"
)

// ConnectorSource supplies a user's enabled external MCP connectors at run time.
type ConnectorSource func(ctx context.Context, email string) ([]db.Connector, error)

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
		tools = filterEnabled(tools, req.EnabledTools)
		// skill mgmt + artifact publishing + quant pipeline are always available
		// (local tools).
		local := append(append(SkillTools(), ArtifactTools...), QuantTools...)
		return append(tools, local...), nil, nil
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
	connectors  ConnectorSource // user's external MCP servers (optional)

	mu      sync.Mutex
	entries map[string]*mcpEntry
}

type mcpEntry struct {
	clients  []*mcpclient.Client // gateway + each enabled connector
	tools    []agent.BaseTool    // merged tools bound to this user
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
		closeClients(e.clients)
		delete(p.entries, email)
	}
	e, err := p.connect(ctx, email)
	if err != nil {
		return nil, err
	}
	p.entries[email] = e
	return e.tools, nil
}

// connect creates one independent MCP connection set. Sales BI runs use this
// directly because the connector maintains per-turn flow guards in process;
// sharing that process across chat runs leaks one question's state into the next.
func (p *mcpPool) connect(ctx context.Context, email string) (*mcpEntry, error) {
	tok, err := security.Sign(security.NewToken(email, p.scopes, p.tokenTTL), []byte(p.secret))
	if err != nil {
		return nil, err
	}
	// The built-in gateway (file_*/web_* over MCP) — auth'd with the signed token.
	gateway, err := mcpclient.New(ctx, mcpclient.Config{
		Transport: mcpclient.TransportStreamableHTTP,
		URL:       p.mcpURL,
		Headers:   map[string]string{"X-Orka-Token": tok},
		Name:      "orka-control",
	})
	if err != nil {
		return nil, err
	}
	clients := []*mcpclient.Client{gateway}

	// Each enabled connector is a user-registered external MCP server; its tools
	// join the set. A bad connector is skipped (never fails the whole run).
	if p.connectors != nil {
		conns, _ := p.connectors(ctx, email)
		for _, cn := range conns {
			cl, cerr := mcpclient.New(ctx, connectorConfig(cn))
			if cerr != nil {
				continue
			}
			clients = append(clients, cl)
		}
	}

	// Only the GUI tool is local; everything else comes over MCP. One manager
	// merges the gateway + all connector clients into a single tool list.
	tools, err := toolsmanager.New([]agent.BaseTool{GUITool}, clients...).GetTools(ctx)
	if err != nil {
		closeClients(clients)
		return nil, err
	}
	now := time.Now()
	return &mcpEntry{clients: clients, tools: tools, created: now, lastUsed: now}, nil
}

func (p *mcpPool) dedicated(ctx context.Context, email string) ([]agent.BaseTool, func(), error) {
	e, err := p.connect(ctx, email)
	if err != nil {
		return nil, nil, err
	}
	return e.tools, func() { closeClients(e.clients) }, nil
}

// invalidate drops a user's cached connection set so the next run rebuilds it
// (called when the user adds/removes a connector).
func (p *mcpPool) invalidate(email string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[email]; e != nil {
		closeClients(e.clients)
		delete(p.entries, email)
	}
}

func closeClients(cs []*mcpclient.Client) {
	for _, c := range cs {
		_ = c.Close()
	}
}

// ProbeConnector connects to an MCP server and returns its tool names, so the
// UI can validate a connection before saving it. Always closes the client.
func ProbeConnector(ctx context.Context, cn db.Connector) ([]string, error) {
	cl, err := mcpclient.New(ctx, connectorConfig(cn))
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	tools, err := toolsmanager.New(nil, cl).GetTools(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name())
	}
	return names, nil
}

// connectorConfig maps a stored Connector to an MCP client config.
func connectorConfig(cn db.Connector) mcpclient.Config {
	cfg := mcpclient.Config{Headers: cn.Headers, Command: cn.Command, Args: cn.Args, Name: "orka-connector:" + cn.Name}
	switch cn.Transport {
	case "stdio":
		cfg.Transport = mcpclient.TransportStdio
	case "http":
		cfg.Transport = mcpclient.TransportHTTP
		cfg.URL = cn.URL
	default:
		cfg.Transport = mcpclient.TransportStreamableHTTP
		cfg.URL = cn.URL
	}
	return cfg
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
					closeClients(e.clients)
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
		closeClients(e.clients)
		delete(p.entries, email)
	}
}

// MCPToolsProviderPooled is MCPToolsProvider with a per-user connection pool.
// On any pool error it degrades to local filesystem tools (+ GUI). connectors
// (optional) merges each user's registered external MCP servers. Returns the
// provider plus an invalidate(email) callback to bust a user's cache when their
// connectors change.
func MCPToolsProviderPooled(baseStorage, mcpURL, secret string, tokenTTL time.Duration, scopes []string, connectors ConnectorSource) (ToolsProvider, func(string)) {
	maxAge := tokenTTL - time.Minute
	if maxAge < 30*time.Second {
		maxAge = tokenTTL / 2
	}
	pool := &mcpPool{
		baseStorage: baseStorage, mcpURL: mcpURL, secret: secret,
		tokenTTL: tokenTTL, maxAge: maxAge, scopes: scopes, connectors: connectors,
		entries: map[string]*mcpEntry{},
	}
	go pool.janitor(context.Background()) // evict idle connections for process lifetime
	provider := func(ctx context.Context, req ChatRunRequest) ([]agent.BaseTool, func(), error) {
		var tools []agent.BaseTool
		var cleanup func()
		var err error
		if strings.EqualFold(strings.TrimSpace(req.ActiveSkill), salesBISkillName) {
			tools, cleanup, err = pool.dedicated(ctx, req.UserEmail)
		} else {
			tools, err = pool.get(ctx, req.UserEmail)
		}
		if err != nil {
			root := pathsafe.UserRoot(baseStorage, req.UserEmail)
			fallback := append(filesystem.New(root), GUITool)
			local := append(append(SkillTools(), ArtifactTools...), QuantTools...)
			return append(filterEnabled(fallback, req.EnabledTools), local...), nil, err
		}
		// Pooled runs return no cleanup; Sales BI's dedicated MCP process is closed
		// after this run so its flow state cannot poison a later conversation.
		local := append(append(SkillTools(), ArtifactTools...), QuantTools...)
		return append(filterEnabled(tools, req.EnabledTools), local...), cleanup, nil
	}
	return provider, pool.invalidate
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

// ToolInfo describes one available tool for the UI's tool picker, so the
// frontend can show real descriptions + grouping instead of bare tool ids.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Group       string `json:"group"`
	Danger      bool   `json:"danger"` // runs code or makes network egress — flag it
}

// dangerTools run arbitrary code, reach the network, or commit a consequential
// change; the UI marks them and (when confirm is on) gates them behind human
// approval. ingest_factor is the pipeline's human-review checkpoint: in an
// interactive run it asks before a factor enters the library.
var dangerTools = map[string]bool{"shell": true, "python": true, "run_agent": true, "http_request": true, "ingest_factor": true}

// ToolCatalog returns the tools actually available to a user (gateway + their
// connectors), with descriptions and groups — the single source of truth for
// the tool picker, replacing the frontend's hardcoded list. Derived from the
// live tool set so it never drifts from what the agent can really call.
func (s *ChatService) ToolCatalog(ctx context.Context, email string) []ToolInfo {
	if s.ToolsFor == nil {
		return nil
	}
	tools, cleanup, _ := s.ToolsFor(ctx, ChatRunRequest{UserEmail: email})
	if cleanup != nil {
		defer cleanup()
	}
	out := make([]ToolInfo, 0, len(tools))
	for _, t := range tools {
		out = append(out, ToolInfo{
			Name:        t.Name(),
			Description: t.Description(),
			Group:       toolGroup(t),
			Danger:      dangerTools[t.Name()],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func groupForName(name string) string {
	switch {
	case strings.HasPrefix(name, "file_") || name == "memory":
		return "file"
	case name == "run_agent":
		return "gui_agent"
	case name == "shell":
		return "shell"
	case name == "web_search" || name == "fetch_url" || name == "weather" || name == "http_request":
		return "web"
	case name == "current_time" || name == "calculator" || name == "unit_convert" ||
		name == "base64" || name == "hash" || name == "uuid" || name == "json_format" ||
		name == "text_stats" || name == "regex_extract" || name == "json_query" ||
		name == "datetime" || name == "random":
		return "util"
	case name == "currency" || name == "timezone" || name == "qrcode" ||
		name == "csv_query" || name == "csv_stats" || name == "csv_to_json" ||
		name == "doc_export" || name == "chart" ||
		name == "xlsx_to_csv" || name == "csv_to_xlsx" || name == "pdf_extract" ||
		name == "doc_read" || name == "sql_query" || name == "csv_join" || name == "slides":
		return "office"
	case name == "python":
		return "code"
	case name == "artifact_publish" || name == "artifact_get":
		return "artifact"
	case strings.HasPrefix(name, "find_skills") || strings.HasPrefix(name, "skill_"):
		return "skill"
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
