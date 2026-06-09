package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// searchProvider runs a query and returns formatted results, or "" if it is not
// configured / fails (so the caller falls through to the next provider).
type searchProvider func(ctx context.Context, q string, limit int) string

// searchProviders returns the configured premium providers in priority order.
// Each is keyless-safe: it returns "" when its env var is unset, so the default
// keyless DuckDuckGo/Wikipedia path stays the zero-config behaviour.
func searchProviders() []searchProvider {
	var ps []searchProvider
	if os.Getenv("SERPER_API_KEY") != "" {
		ps = append(ps, serperSearch)
	}
	if os.Getenv("BRAVE_API_KEY") != "" {
		ps = append(ps, braveSearch)
	}
	if os.Getenv("SEARXNG_URL") != "" {
		ps = append(ps, searxngSearch)
	}
	return ps
}

func formatResults(provider, q string, items [][3]string) string {
	if len(items) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Top results for %q (%s):\n\n", q, provider)
	for i, it := range items {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, it[0], it[1])
		if it[2] != "" {
			fmt.Fprintf(&sb, "   %s\n", it[2])
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// serperSearch uses serper.dev (Google results). Set SERPER_API_KEY.
func serperSearch(ctx context.Context, q string, limit int) string {
	body, _ := json.Marshal(map[string]any{"q": q, "num": limit})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://google.serper.dev/search", bytes.NewReader(body))
	req.Header.Set("X-API-KEY", os.Getenv("SERPER_API_KEY"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpSearchC.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var r struct {
		Organic []struct {
			Title, Link, Snippet string
		} `json:"organic"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	items := make([][3]string, 0, limit)
	for i, o := range r.Organic {
		if i >= limit {
			break
		}
		items = append(items, [3]string{o.Title, o.Link, o.Snippet})
	}
	return formatResults("serper", q, items)
}

// braveSearch uses the Brave Search API. Set BRAVE_API_KEY.
func braveSearch(ctx context.Context, q string, limit int) string {
	endpoint := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d", url.QueryEscape(q), limit)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("X-Subscription-Token", os.Getenv("BRAVE_API_KEY"))
	req.Header.Set("Accept", "application/json")
	resp, err := httpSearchC.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var r struct {
		Web struct {
			Results []struct {
				Title, URL, Description string
			} `json:"results"`
		} `json:"web"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	items := make([][3]string, 0, limit)
	for i, o := range r.Web.Results {
		if i >= limit {
			break
		}
		items = append(items, [3]string{o.Title, o.URL, clean(o.Description)})
	}
	return formatResults("brave", q, items)
}

// searxngSearch uses a self-hosted SearXNG instance. Set SEARXNG_URL (base URL).
func searxngSearch(ctx context.Context, q string, limit int) string {
	base := strings.TrimRight(os.Getenv("SEARXNG_URL"), "/")
	endpoint := fmt.Sprintf("%s/search?q=%s&format=json", base, url.QueryEscape(q))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "CavisBot/0.1")
	resp, err := httpSearchC.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var r struct {
		Results []struct {
			Title, URL, Content string
		} `json:"results"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	items := make([][3]string, 0, limit)
	for i, o := range r.Results {
		if i >= limit {
			break
		}
		items = append(items, [3]string{o.Title, o.URL, clean(o.Content)})
	}
	return formatResults("searxng", q, items)
}
