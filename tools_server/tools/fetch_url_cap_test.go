package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// The cap here has to stay under the control layer's truncation threshold, not
// far under it. At 4,000 it was six times stricter, and the agent routed around
// it: needing more of a documentation page, it used http_request for the RAW
// body and shelled out to python to strip the markup — 29 of one orchestrator's
// 29 shell calls were re-implementing this function on this function's output.
func TestFetchBodyCapIsNotStricterThanTheContextLayer(t *testing.T) {
	// The control layer truncates a tool result past this and offloads the rest
	// (service.maxToolOutputChars); named here because the gateway keeps no
	// dependency on it.
	const contextLayerTruncChars = 24000

	if maxFetchBodyChars > contextLayerTruncChars {
		t.Fatalf("cap %d exceeds the context layer's %d, so a page is cut by the wrong "+
			"layer and never offloaded cleanly", maxFetchBodyChars, contextLayerTruncChars)
	}
	if maxFetchBodyChars < contextLayerTruncChars/2 {
		t.Errorf("cap %d discards content the context layer (%d) would have accepted, "+
			"which is what pushed the agent to http_request plus manual HTML parsing",
			maxFetchBodyChars, contextLayerTruncChars)
	}
}

// Truncation must be visible: an agent that cannot tell a page continues will
// answer from the part it happened to receive.
func TestFetchURLTruncationIsVisibleAndGenerous(t *testing.T) {
	long := strings.Repeat("documentation sentence. ", 3000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><title>Doc</title><body>" + long + "</body></html>"))
	}))
	defer srv.Close()

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"url": srv.URL}
	res, err := fetchURL()(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var out string
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			out += tc.Text
		}
	}
	if !strings.Contains(out, "…") {
		t.Error("truncation is silent; the agent cannot tell the page continues")
	}
	if len(out) < 10000 {
		t.Errorf("only %d chars returned from a long page; the old 4k cap is what drove "+
			"the agent to fetch raw HTML and parse it itself", len(out))
	}
	// Markup must not survive — the whole point is that the agent never needs to
	// strip it.
	if strings.Contains(out, "<html") || strings.Contains(out, "<body") {
		t.Error("HTML leaked into the extracted text")
	}
}
