// Package tools defines the gateway's tools: real file tools confined to the
// per-user storage root, plus Lark/AIO stubs. Each tool carries a ToolGroup and
// a required RBAC scope.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/cavis-oss/tools_server/identity"
	"github.com/cavis-oss/tools_server/util"
)

// Meta describes a tool's group and the scope required to use it.
type Meta struct {
	Group string
	Scope string
}

// Registry maps tool name -> Meta. Used by the gateway's tool filter (list) and
// the per-handler guard (call).
func Registry() map[string]Meta {
	return map[string]Meta{
		"file_read":   {Group: "file", Scope: "file:read"},
		"file_write":  {Group: "file", Scope: "file:write"},
		"file_list":   {Group: "file", Scope: "file:read"},
		"lark_whoami": {Group: "lark", Scope: "lark:read"},
		"aio_echo":    {Group: "aio", Scope: "aio:read"},
	}
}

// Register adds all non-blacklisted tools to the server, wrapping handlers with
// a scope guard (defense in depth: list filtering is not enough on its own).
func Register(s *mcpserver.MCPServer, baseStorage string, blacklist map[string]bool) {
	reg := Registry()
	add := func(tool mcp.Tool, h mcpserver.ToolHandlerFunc) {
		if blacklist[tool.Name] {
			return
		}
		s.AddTool(tool, guard(reg[tool.Name].Scope, h))
	}

	add(mcp.NewTool("file_read",
		mcp.WithDescription("Read a UTF-8 text file from your storage."),
		mcp.WithString("path", mcp.Required(), mcp.Description("relative file path")),
	), fileRead(baseStorage))

	add(mcp.NewTool("file_write",
		mcp.WithDescription("Write a UTF-8 text file to your storage (creates dirs)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("relative file path")),
		mcp.WithString("content", mcp.Required(), mcp.Description("file content")),
	), fileWrite(baseStorage))

	add(mcp.NewTool("file_list",
		mcp.WithDescription("List directory entries in your storage."),
		mcp.WithString("path", mcp.Description("relative directory path (default root)")),
	), fileList(baseStorage))

	add(mcp.NewTool("lark_whoami",
		mcp.WithDescription("Lark: return the current user (stub)."),
	), stub("lark"))

	add(mcp.NewTool("aio_echo",
		mcp.WithDescription("AIO: echo (stub)."),
		mcp.WithString("text", mcp.Description("text")),
	), stub("aio"))
}

// guard enforces the required scope before running the handler.
func guard(scope string, h mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if scope != "" && !identity.From(ctx).HasScope(scope) {
			return mcp.NewToolResultError("permission denied: missing scope " + scope), nil
		}
		return h(ctx, req)
	}
}

func fileRead(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, err := util.ResolvePath(base, identity.From(ctx).Email, req.GetString("path", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(b)), nil
	}
}

func fileWrite(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rel := req.GetString("path", "")
		p, err := util.ResolvePath(base, identity.From(ctx).Email, rel)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		content := req.GetString("content", "")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("wrote %d bytes to %s", len(content), rel)), nil
	}
}

func fileList(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rel := req.GetString("path", "")
		if rel == "" {
			rel = "."
		}
		p, err := util.ResolvePath(base, identity.From(ctx).Email, rel)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		var sb strings.Builder
		for _, e := range entries {
			kind := "f"
			if e.IsDir() {
				kind = "d"
			}
			fmt.Fprintf(&sb, "%s\t%s\n", kind, e.Name())
		}
		return mcp.NewToolResultText(sb.String()), nil
	}
}

func stub(name string) mcpserver.ToolHandlerFunc {
	return func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(fmt.Sprintf("[%s] connector not configured in this OSS build", name)), nil
	}
}
