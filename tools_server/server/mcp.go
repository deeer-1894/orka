// Package server builds the MCP gateway: it aggregates tools, filters tools/list
// by ToolGroup blacklist + RBAC scope, and injects the verified caller identity
// from a signed context token.
package server

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/orka-oss/orka_core/security"
	"github.com/orka-oss/tools_server/identity"
	"github.com/orka-oss/tools_server/tools"
)

// Config configures the gateway.
type Config struct {
	Secret      string   // HMAC secret for verifying context tokens
	BaseStorage string   // root for per-user file storage
	Blacklist   []string // globally disabled tool names
}

// New builds the MCP server (without HTTP transport).
func New(cfg Config) *mcpserver.MCPServer {
	bl := toSet(cfg.Blacklist)
	reg := tools.Registry()

	s := mcpserver.NewMCPServer("orka-tools", "0.1.0",
		mcpserver.WithToolCapabilities(true),
		// tools/list filter: drop tools the caller's scopes don't grant.
		mcpserver.WithToolFilter(func(ctx context.Context, ts []mcp.Tool) []mcp.Tool {
			id := identity.From(ctx)
			out := make([]mcp.Tool, 0, len(ts))
			for _, t := range ts {
				m := reg[t.Name]
				if m.Scope != "" && !id.HasScope(m.Scope) {
					continue
				}
				out = append(out, t)
			}
			return out
		}),
	)
	tools.Register(s, cfg.BaseStorage, bl)
	return s
}

// ContextFunc verifies the signed context token from the X-Orka-Token header
// and injects the caller identity. An X-User-Email header (without a valid
// token) yields an identity with NO scopes, so scoped tools are denied.
func ContextFunc(secret []byte) mcpserver.HTTPContextFunc {
	return func(ctx context.Context, r *http.Request) context.Context {
		if tok := r.Header.Get("X-Orka-Token"); tok != "" {
			if ct, err := security.Verify(tok, secret); err == nil {
				return identity.With(ctx, identity.Identity{Email: ct.UserEmail, Scopes: ct.Scopes})
			}
		}
		if email := r.Header.Get("X-User-Email"); email != "" {
			return identity.With(ctx, identity.Identity{Email: email}) // untrusted: no scopes
		}
		return ctx
	}
}

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		if x != "" {
			m[x] = true
		}
	}
	return m
}
