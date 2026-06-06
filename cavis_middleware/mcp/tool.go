package mcp

import (
	"context"

	"github.com/cavis-oss/cavis_core/agent"
)

// remoteTool adapts a remote MCP tool to agent.BaseTool. The upper layers can't
// tell it apart from a local tool.
type remoteTool struct {
	client *Client
	name   string
	desc   string
	schema map[string]any
}

var _ agent.BaseTool = (*remoteTool)(nil)

func (t *remoteTool) Name() string             { return t.name }
func (t *remoteTool) Description() string       { return t.desc }
func (t *remoteTool) Schema() map[string]any    { return t.schema }
func (t *remoteTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	return t.client.call(ctx, t.name, args)
}
