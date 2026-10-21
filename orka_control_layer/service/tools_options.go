package service

import (
	"context"

	"github.com/orka-oss/orka_control_layer/browsertool"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/agent"
)

// ToolsProviderOptions binds immutable built-ins to a provider instance. The
// same tools serve local, MCP and fallback paths; construction performs no I/O.
type ToolsProviderOptions struct {
	GUI     agent.BaseTool
	Browser agent.BaseTool
	// ConnectorAuth supplies per-request headers for a connector the user
	// authorized with OAuth (a fresh access token); nil leaves them static.
	ConnectorAuth ConnectorAuth
}

// ConnectorAuth returns the headers one connector's requests carry right now.
type ConnectorAuth func(ctx context.Context, cn db.Connector) (map[string]string, error)

func connectorAuthOf(options []ToolsProviderOptions) ConnectorAuth {
	for _, opt := range options {
		if opt.ConnectorAuth != nil {
			return opt.ConnectorAuth
		}
	}
	return nil
}

func providerBuiltins(base string, options []ToolsProviderOptions) []agent.BaseTool {
	var gui agent.BaseTool = guiUnavailableTool{}
	var browser agent.BaseTool = browsertool.New(nil, base)
	for _, opt := range options {
		if opt.GUI != nil {
			gui = opt.GUI
		}
		if opt.Browser != nil {
			browser = opt.Browser
		}
	}
	return []agent.BaseTool{gui, browser}
}
