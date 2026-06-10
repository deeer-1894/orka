// Package toolsmanager unifies local tools and remote MCP tools into a single
// []agent.BaseTool, so the agent runtime never has to care where a tool lives.
package toolsmanager

import (
	"context"
	"fmt"

	"github.com/orka-oss/orka_core/agent"
	mcpclient "github.com/orka-oss/orka_middleware/mcp"
)

// ToolsManager aggregates local tools and MCP clients.
type ToolsManager struct {
	local   []agent.BaseTool
	clients []*mcpclient.Client
}

// New builds a ToolsManager from local tools and zero or more MCP clients.
func New(local []agent.BaseTool, clients ...*mcpclient.Client) *ToolsManager {
	return &ToolsManager{local: local, clients: clients}
}

// GetTools returns local tools followed by tools discovered from each MCP
// client. A failure to list one upstream is returned (caller may choose to
// degrade); local tools are always included first.
func (m *ToolsManager) GetTools(ctx context.Context) ([]agent.BaseTool, error) {
	out := make([]agent.BaseTool, 0, len(m.local))
	out = append(out, m.local...)
	for _, c := range m.clients {
		ts, err := c.ListTools(ctx)
		if err != nil {
			return out, fmt.Errorf("toolsmanager: list mcp tools: %w", err)
		}
		out = append(out, ts...)
	}
	return out, nil
}

// Close closes all MCP clients.
func (m *ToolsManager) Close() {
	for _, c := range m.clients {
		_ = c.Close()
	}
}
