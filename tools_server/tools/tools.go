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

	"github.com/orka-oss/tools_server/identity"
	"github.com/orka-oss/tools_server/util"
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
		"web_search":   {Group: "web", Scope: "web:search"},
		"fetch_url":    {Group: "web", Scope: "web:search"},
		"weather":      {Group: "web", Scope: "web:search"},
		"current_time": {Group: "util", Scope: ""}, // always available
		"calculator":   {Group: "util", Scope: ""},
		"unit_convert": {Group: "util", Scope: ""},
		"http_request": {Group: "web", Scope: "web:search"}, // network egress → gated
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

	add(mcp.NewTool("web_search",
		mcp.WithDescription("Search the web (DuckDuckGo) and return the top results. Use this for facts, weather, news, docs — not the GUI browser."),
		mcp.WithString("query", mcp.Required(), mcp.Description("search query")),
		mcp.WithNumber("limit", mcp.Description("max results (default 5)")),
	), webSearch())

	add(mcp.NewTool("fetch_url",
		mcp.WithDescription("Fetch a web page and return its readable text. Use after web_search to read a result."),
		mcp.WithString("url", mcp.Required(), mcp.Description("the page URL")),
	), fetchURL())

	add(mcp.NewTool("weather",
		mcp.WithDescription("Get current weather + today's forecast for a location (live, keyless)."),
		mcp.WithString("location", mcp.Required(), mcp.Description("city name, e.g. 西安 / Xian")),
	), weather())

	add(mcp.NewTool("current_time",
		mcp.WithDescription("Get the current date, time and weekday. Use for any 'today/now/recent' question."),
		mcp.WithString("timezone", mcp.Description("IANA tz, default Asia/Shanghai")),
	), currentTime())

	add(mcp.NewTool("calculator",
		mcp.WithDescription("Evaluate an arithmetic expression: + - * / % ^ and parentheses."),
		mcp.WithString("expression", mcp.Required(), mcp.Description("e.g. (3+4)*2^3")),
	), calculator())

	add(mcp.NewTool("unit_convert",
		mcp.WithDescription("Convert a value between units (length, mass, data, time, temperature)."),
		mcp.WithNumber("value", mcp.Required(), mcp.Description("the numeric value")),
		mcp.WithString("from", mcp.Required(), mcp.Description("source unit, e.g. km, lb, GiB, C")),
		mcp.WithString("to", mcp.Required(), mcp.Description("target unit, e.g. mi, kg, MB, F")),
	), unitConvert())

	add(mcp.NewTool("http_request",
		mcp.WithDescription("Make an HTTP GET/POST to a public URL (e.g. a JSON API) and return status + body. Not for browser tasks."),
		mcp.WithString("url", mcp.Required(), mcp.Description("the request URL (http/https, public host only)")),
		mcp.WithString("method", mcp.Description("GET (default) or POST")),
		mcp.WithString("body", mcp.Description("request body for POST")),
		mcp.WithString("content_type", mcp.Description("Content-Type for the body")),
	), httpRequest())

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
