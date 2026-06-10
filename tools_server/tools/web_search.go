package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

var (
	reResultA   = regexp.MustCompile(`(?s)class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	reSnippet   = regexp.MustCompile(`(?s)class="result__snippet"[^>]*>(.*?)</a>`)
	reTags      = regexp.MustCompile(`<[^>]+>`)
	httpSearchC = &http.Client{Timeout: 15 * time.Second}
)

// webSearch returns the top web results as text. It tries a configured premium
// provider first (set one of SERPER_API_KEY / BRAVE_API_KEY / SEARXNG_URL for
// stable, high-quality results), then falls back to keyless DuckDuckGo scraping
// and finally the Wikipedia API. This is the right tool for information lookups
// (facts, docs, news) — far cheaper and more reliable than driving a browser.
func webSearch() mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q := strings.TrimSpace(req.GetString("query", ""))
		if q == "" {
			return mcp.NewToolResultError("query is required"), nil
		}
		limit := req.GetInt("limit", 5)
		if limit <= 0 || limit > 10 {
			limit = 5
		}

		// Premium providers first (only if their key/URL is configured).
		for _, p := range searchProviders() {
			if out := p(ctx, q, limit); out != "" {
				return mcp.NewToolResultText(out), nil
			}
		}

		endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(q)
		hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		hreq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; OrkaBot/0.1)")
		resp, err := httpSearchC.Do(hreq)
		if err != nil {
			return mcp.NewToolResultError("search failed: " + err.Error()), nil
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

		titles := reResultA.FindAllStringSubmatch(string(body), limit)
		snips := reSnippet.FindAllStringSubmatch(string(body), limit)
		if len(titles) == 0 {
			// DuckDuckGo throttles scraping; fall back to the keyless Wikipedia
			// search API so fact lookups still return something useful.
			if wiki := wikiSearch(ctx, q, limit); wiki != "" {
				return mcp.NewToolResultText(wiki), nil
			}
			return mcp.NewToolResultText("No results for: " + q), nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Top results for %q:\n\n", q)
		for i, t := range titles {
			title := clean(t[2])
			link := decodeDDG(t[1])
			fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, title, link)
			if i < len(snips) {
				if s := clean(snips[i][1]); s != "" {
					fmt.Fprintf(&sb, "   %s\n", s)
				}
			}
			sb.WriteString("\n")
		}
		return mcp.NewToolResultText(sb.String()), nil
	}
}

func clean(s string) string {
	s = reTags.ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}

// wikiSearch queries the keyless Wikipedia search API (zh, then en). Reliable
// and never rate-limited, so it makes a solid fallback for DuckDuckGo.
func wikiSearch(ctx context.Context, q string, limit int) string {
	for _, lang := range []string{"zh", "en"} {
		api := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json&srlimit=%d",
			lang, url.QueryEscape(q), limit)
		hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
		hreq.Header.Set("User-Agent", "OrkaBot/0.1 (https://example.com)")
		resp, err := httpSearchC.Do(hreq)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		var r struct {
			Query struct {
				Search []struct {
					Title   string `json:"title"`
					Snippet string `json:"snippet"`
				} `json:"search"`
			} `json:"query"`
		}
		if json.Unmarshal(body, &r) != nil || len(r.Query.Search) == 0 {
			continue
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Wikipedia results for %q:\n\n", q)
		for i, s := range r.Query.Search {
			fmt.Fprintf(&sb, "%d. %s\n   %s\n   https://%s.wikipedia.org/wiki/%s\n\n",
				i+1, s.Title, clean(s.Snippet), lang, url.PathEscape(strings.ReplaceAll(s.Title, " ", "_")))
		}
		return sb.String()
	}
	return ""
}

// decodeDDG turns //duckduckgo.com/l/?uddg=<encoded>&rut=... into the real URL.
func decodeDDG(href string) string {
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if real := u.Query().Get("uddg"); real != "" {
		if dec, err := url.QueryUnescape(real); err == nil {
			return dec
		}
	}
	return href
}
