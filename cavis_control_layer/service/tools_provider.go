package service

import (
	"context"
	"time"

	"github.com/cavis-oss/cavis_core/agent"
	"github.com/cavis-oss/cavis_core/pathsafe"
	"github.com/cavis-oss/cavis_core/security"
	"github.com/cavis-oss/cavis_middleware/local/filesystem"
	mcpclient "github.com/cavis-oss/cavis_middleware/mcp"
	"github.com/cavis-oss/cavis_middleware/toolsmanager"
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

// MCPToolsProvider serves local tools plus tools discovered from a remote
// tools_server over MCP (streamable HTTP). It signs a short-lived context token
// carrying the user's scopes so the gateway can verify identity + apply RBAC.
// On connection failure it degrades to local tools.
func MCPToolsProvider(baseStorage, mcpURL, secret string, tokenTTL time.Duration, scopes []string) ToolsProvider {
	return func(ctx context.Context, req ChatRunRequest) ([]agent.BaseTool, func(), error) {
		root := pathsafe.UserRoot(baseStorage, req.UserEmail)
		// Degrade fallback: local filesystem tools + GUI tool when MCP is down.
		fallback := append(filesystem.New(root), GUITool)
		// Merge set: file tools come from the gateway over MCP; only the GUI
		// tool is local here (avoids duplicate file_* names).
		mergeLocal := []agent.BaseTool{GUITool}

		tok, err := security.Sign(security.NewToken(req.UserEmail, scopes, tokenTTL), []byte(secret))
		if err != nil {
			return filterEnabled(fallback, req.EnabledTools), nil, err
		}
		client, err := mcpclient.New(ctx, mcpclient.Config{
			Transport: mcpclient.TransportStreamableHTTP,
			URL:       mcpURL,
			Headers:   map[string]string{"X-Cavis-Token": tok},
			Name:      "cavis-control",
		})
		if err != nil {
			return filterEnabled(fallback, req.EnabledTools), nil, err
		}
		tm := toolsmanager.New(mergeLocal, client)
		tools, err := tm.GetTools(ctx)
		if err != nil {
			_ = client.Close()
			return filterEnabled(fallback, req.EnabledTools), nil, err
		}
		return filterEnabled(tools, req.EnabledTools), func() { _ = client.Close() }, nil
	}
}

// filterEnabled keeps the tools matching the request's enabled set. An empty set
// means "all". Group aliases: "file" -> file_*, "gui_agent" -> run_agent.
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
		name := t.Name()
		switch {
		case want[name]:
		case want["file"] && len(name) >= 5 && name[:5] == "file_":
		case want["gui_agent"] && name == "run_agent":
		default:
			continue
		}
		out = append(out, t)
	}
	return out
}
