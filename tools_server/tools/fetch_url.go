package tools

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

var (
	// RE2 has no backreferences — match script/style separately.
	reScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reTitle  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

// fetchURL downloads a page and returns its readable text. Pairs with
// web_search: search to find a link, fetch_url to read it — no GUI needed.
func fetchURL() mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		u := strings.TrimSpace(req.GetString("url", ""))
		if u == "" {
			return mcp.NewToolResultError("url is required"), nil
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			u = "https://" + u
		}
		hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		hreq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CavisBot/0.1)")
		resp, err := httpSearchC.Do(hreq)
		if err != nil {
			return mcp.NewToolResultError("fetch failed: " + err.Error()), nil
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		html := string(raw)

		title := ""
		if m := reTitle.FindStringSubmatch(html); len(m) > 1 {
			title = clean(m[1])
		}
		body := reScript.ReplaceAllString(html, " ")
		body = reStyle.ReplaceAllString(body, " ")
		body = clean(body)
		if len(body) > 4000 {
			body = body[:4000] + "…"
		}
		out := "URL: " + u + "\n"
		if title != "" {
			out += "Title: " + title + "\n"
		}
		out += "\n" + body
		return mcp.NewToolResultText(out), nil
	}
}
