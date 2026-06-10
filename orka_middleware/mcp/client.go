// Package mcp is a multi-transport MCP client. Transport differences
// (http/sse, streamable_http, stdio) are hidden behind one Client so the upper
// layers only see agent.BaseTool. It supports a dynamic header/token provider.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	mcpgo "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/orka-oss/orka_core/agent"
)

// Transport selects the wire protocol.
type Transport string

const (
	TransportHTTP           Transport = "http"            // SSE
	TransportStreamableHTTP Transport = "streamable_http" // streamable HTTP
	TransportStdio          Transport = "stdio"           // local subprocess
)

// Config configures an MCP client connection.
type Config struct {
	Transport Transport
	URL       string // for http/streamable_http
	Headers   map[string]string
	// HeaderFunc, if set, supplies headers per request (e.g. a fresh signed
	// context token), enabling dynamic auth without rebuilding the client.
	HeaderFunc func(context.Context) map[string]string
	Command    string   // for stdio
	Args       []string // for stdio
	Env        []string // for stdio
	Name       string   // client identity
}

// Client wraps an mcp-go client with our conveniences.
type Client struct {
	raw  *mcpgo.Client
	name string
}

// New builds, starts and initializes an MCP client for the given transport.
func New(ctx context.Context, cfg Config) (*Client, error) {
	raw, err := dial(cfg)
	if err != nil {
		return nil, err
	}
	if err := raw.Start(ctx); err != nil {
		return nil, fmt.Errorf("mcp start: %w", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	name := cfg.Name
	if name == "" {
		name = "orka-middleware"
	}
	initReq.Params.ClientInfo = mcp.Implementation{Name: name, Version: "0.1.0"}
	if _, err := raw.Initialize(ctx, initReq); err != nil {
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	return &Client{raw: raw, name: name}, nil
}

// dial picks the transport constructor (the transport_adapter).
func dial(cfg Config) (*mcpgo.Client, error) {
	switch cfg.Transport {
	case TransportStdio:
		if cfg.Command == "" {
			return nil, fmt.Errorf("mcp stdio: command required")
		}
		return mcpgo.NewStdioMCPClient(cfg.Command, cfg.Env, cfg.Args...)
	case TransportStreamableHTTP:
		return mcpgo.NewStreamableHttpClient(cfg.URL, streamOpts(cfg)...)
	case TransportHTTP, "":
		return mcpgo.NewSSEMCPClient(cfg.URL, sseOpts(cfg)...)
	default:
		return nil, fmt.Errorf("mcp: unknown transport %q", cfg.Transport)
	}
}

// sseOpts builds header options for the SSE transport.
func sseOpts(cfg Config) []transport.ClientOption {
	var opts []transport.ClientOption
	if cfg.HeaderFunc != nil {
		opts = append(opts, mcpgo.WithHeaderFunc(transport.HTTPHeaderFunc(cfg.HeaderFunc)))
	} else if len(cfg.Headers) > 0 {
		opts = append(opts, mcpgo.WithHeaders(cfg.Headers))
	}
	return opts
}

// streamOpts builds header options for the streamable-HTTP transport.
func streamOpts(cfg Config) []transport.StreamableHTTPCOption {
	var opts []transport.StreamableHTTPCOption
	if cfg.HeaderFunc != nil {
		opts = append(opts, transport.WithHTTPHeaderFunc(transport.HTTPHeaderFunc(cfg.HeaderFunc)))
	} else if len(cfg.Headers) > 0 {
		opts = append(opts, transport.WithHTTPHeaders(cfg.Headers))
	}
	return opts
}

// ListTools returns the upstream tools wrapped as agent.BaseTool.
func (c *Client) ListTools(ctx context.Context) ([]agent.BaseTool, error) {
	res, err := c.raw.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("mcp list tools: %w", err)
	}
	out := make([]agent.BaseTool, 0, len(res.Tools))
	for _, t := range res.Tools {
		out = append(out, &remoteTool{
			client: c,
			name:   t.Name,
			desc:   t.Description,
			schema: schemaToMap(t.InputSchema),
		})
	}
	return out, nil
}

// call invokes a tool by name and returns its text content.
func (c *Client) call(ctx context.Context, name string, args map[string]any) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.raw.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("mcp call %q: %w", name, err)
	}
	text := extractText(res)
	if res.IsError {
		return "", fmt.Errorf("tool %q error: %s", name, text)
	}
	return text, nil
}

// Close shuts down the underlying client.
func (c *Client) Close() error { return c.raw.Close() }

func extractText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, ct := range res.Content {
		switch v := ct.(type) {
		case mcp.TextContent:
			sb.WriteString(v.Text)
		case *mcp.TextContent:
			sb.WriteString(v.Text)
		}
	}
	return sb.String()
}

func schemaToMap(s mcp.ToolInputSchema) map[string]any {
	b, err := json.Marshal(s)
	if err != nil {
		slog.Warn("mcp: tool schema marshal failed; using empty object", "err", err)
		return map[string]any{"type": "object"}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		slog.Warn("mcp: tool schema unmarshal failed; using empty object", "err", err)
		return map[string]any{"type": "object"}
	}
	return m
}
