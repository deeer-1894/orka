package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/orka-oss/orka_core/agent"
)

// remoteTool adapts a remote MCP tool to agent.BaseTool. The upper layers can't
// tell it apart from a local tool.
type remoteTool struct {
	client    *Client
	name      string
	qualified string
	desc      string
	schema    map[string]any
}

var _ agent.BaseTool = (*remoteTool)(nil)

func (t *remoteTool) Name() string {
	if t.qualified != "" {
		return t.qualified
	}
	return t.name
}
func (t *remoteTool) Description() string    { return t.desc }
func (t *remoteTool) Schema() map[string]any { return t.schema }
func (t *remoteTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	return t.client.call(ctx, t.name, args)
}

// qualifiedToolName keeps provider names within the common 64-byte function ID
// limit while preserving a stable, collision-resistant connection identity.
func qualifiedToolName(source, name string) string {
	if source == "" {
		return name
	}
	digest := sha256.Sum256([]byte(source))
	prefix := "mcp_" + hex.EncodeToString(digest[:6]) + "__"
	cleaned := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, name)
	if len(cleaned) > 38 || cleaned != name {
		hash := sha256.Sum256([]byte(name))
		if len(cleaned) > 24 {
			cleaned = cleaned[:24]
		}
		cleaned += "_" + hex.EncodeToString(hash[:6])
	}
	return prefix + cleaned
}
func (t *remoteTool) Group() string {
	if t.client != nil && t.client.namespace != "" {
		return "integration"
	}
	return ""
}
