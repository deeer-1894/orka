package mcp

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/cavis-oss/cavis_core/agent"
	"github.com/cavis-oss/cavis_middleware/internal/echo"
)

func findTool(tools []agent.BaseTool, name string) agent.BaseTool {
	for _, t := range tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

// exercise lists tools and calls echo, asserting the round-trip.
func exercise(t *testing.T, c *Client) {
	t.Helper()
	ctx := context.Background()
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	echoTool := findTool(tools, "echo")
	if echoTool == nil {
		t.Fatalf("echo tool not listed; got %d tools", len(tools))
	}
	out, err := echoTool.Invoke(ctx, map[string]any{"text": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "echo:hi" {
		t.Fatalf("echo result = %q", out)
	}
}

func TestTransport_StreamableHTTP(t *testing.T) {
	ts := server.NewTestStreamableHTTPServer(echo.Server())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := New(ctx, Config{Transport: TransportStreamableHTTP, URL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exercise(t, c)
}

func TestTransport_SSE(t *testing.T) {
	ts := server.NewTestServer(echo.Server())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// the SSE transport connects to the explicit /sse endpoint
	c, err := New(ctx, Config{Transport: TransportHTTP, URL: ts.URL + "/sse"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exercise(t, c)
}

func TestTransport_Stdio(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "echoserver")
	build := exec.Command("go", "build", "-o", bin, "github.com/cavis-oss/cavis_middleware/internal/echoserver")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build stdio fixture: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := New(ctx, Config{Transport: TransportStdio, Command: bin})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exercise(t, c)
}
