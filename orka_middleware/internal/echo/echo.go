// Package echo provides a tiny MCP server with a single "echo" tool, shared by
// the stdio fixture binary and the transport tests.
package echo

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server builds an MCP server exposing the "echo" tool.
func Server() *server.MCPServer {
	s := server.NewMCPServer("echo", "0.1.0")
	s.AddTool(
		mcp.NewTool("echo",
			mcp.WithDescription("Echo back the provided text."),
			mcp.WithString("text", mcp.Required(), mcp.Description("text to echo")),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("echo:" + req.GetString("text", "")), nil
		},
	)
	return s
}
