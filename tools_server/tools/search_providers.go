package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// searchProvider runs a query and returns formatted results, or "" if it is not
// configured / fails (so the caller falls through to the next provider).
type searchProvider func(ctx context.Context, q string, limit int) string

// searchProviders returns the configured premium providers in priority order.
// Each is keyless-safe: it returns "" when its env var is unset, so the default
// keyless DuckDuckGo/Wikipedia path stays the zero-config behaviour.
func searchProviders() []searchProvider {
	var ps []searchProvider
	if os.Getenv("DOUBAO_SEARCH_API_KEY") != "" {
		ps = append(ps, doubaoSearch)
	}
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

// logSearchFailure makes a provider's failure visible. Returning "" on any error
// is right — the caller must be free to try the next provider — but doing it
// silently meant a throttled premium provider was indistinguishable from an
// unconfigured one, and the agent was told "search is unavailable, configure a
// key" while a perfectly good key was sitting in its environment.
func logSearchFailure(provider, q, reason string) {
	slog.Default().Warn("search provider failed",
		"provider", provider, "reason", reason, "query", trunc(q, 60))
}

func trimBody(b []byte) string { return trunc(strings.TrimSpace(string(b)), 160) }

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

// doubaoSearch uses Volcengine's 豆包搜索 Custom API. Set DOUBAO_SEARCH_API_KEY.
//
// Listed first because it is the provider that works from inside mainland China,
// where the keyless DuckDuckGo/Wikipedia fallback is blocked outright — measured
// on this deployment, web_search returned "temporarily unavailable" on 390 of 391
// calls while being the single most-called tool at 25.4% of all calls.
//
// It returns two text fields per hit and the choice between them matters: the
// API's own docs describe Snippet (~200 chars) as "强烈不建议用于大模型场景" and
// Summary (500-1000 chars) as the one recommended for model use, because Summary
// is the part of the page actually relevant to the query rather than a display
// teaser. Summary is preferred with Snippet as the fallback.
//
// Free tier is 500 calls per account per month, so a misconfigured loop is
// capped by the provider rather than silently expensive.
func doubaoSearch(ctx context.Context, q string, limit int) string {
	return doubaoSearchAt(ctx, "https://open.feedcoopapi.com/search_api/web_search", q, limit)
}

// doubaoSearchAt is doubaoSearch with the endpoint injected, so the response
// parsing — which field wins, what a failure yields — is testable without the
// live API or a key.
func doubaoSearchAt(ctx context.Context, endpoint, q string, limit int) string {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 { // API maximum
		limit = 50
	}
	body, _ := json.Marshal(map[string]any{
		"Query":      q,
		"SearchType": "web",
		"Count":      limit,
		// A result the agent cannot open is a result it cannot cite, and every
		// caller here goes on to fetch_url the link.
		"Filter": map[string]any{"NeedUrl": true},
	})
	// The account-wide QPS limit is the failure that actually happens, because
	// the agent batches: a researcher that emits four searches in one turn fires
	// them concurrently, and the burst is throttled even though the sustained
	// rate is nothing. Observed exactly that — eight searches succeeding over
	// half a minute, then four in the same second all failing. Retry the burst
	// rather than falling through to the keyless endpoints, which are blocked
	// here and turn a throttle into "search is unavailable".
	var raw []byte
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 700 * time.Millisecond):
			case <-ctx.Done():
				return ""
			}
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+os.Getenv("DOUBAO_SEARCH_API_KEY"))
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpSearchC.Do(req)
		if err != nil {
			logSearchFailure("doubao", q, "transport: "+err.Error())
			continue
		}
		raw, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		code := resp.StatusCode
		resp.Body.Close()
		if code == http.StatusTooManyRequests || code >= 500 {
			logSearchFailure("doubao", q, "http "+strconv.Itoa(code)+" (retrying)")
			raw = nil
			continue
		}
		if code/100 != 2 {
			logSearchFailure("doubao", q, "http "+strconv.Itoa(code)+": "+trimBody(raw))
			return ""
		}
		break
	}
	if raw == nil {
		return "" // burst never got through; the caller falls back and says so
	}
	var r struct {
		ResponseMetadata struct {
			Error *struct {
				Code    any    `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
		Result struct {
			WebResults []struct {
				Title, Url, Snippet, Summary string
			} `json:"WebResults"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		logSearchFailure("doubao", q, "decode: "+err.Error())
		return ""
	}
	// A 200 can still carry an application error (empty query, quota, bad key).
	if e := r.ResponseMetadata.Error; e != nil && e.Message != "" {
		logSearchFailure("doubao", q, "api: "+e.Message)
		return ""
	}
	items := make([][3]string, 0, limit)
	for i, o := range r.Result.WebResults {
		if i >= limit {
			break
		}
		text := o.Summary
		if strings.TrimSpace(text) == "" {
			text = o.Snippet
		}
		items = append(items, [3]string{o.Title, o.Url, clean(text)})
	}
	return formatResults("doubao", q, items)
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
	req.Header.Set("User-Agent", "OrkaBot/0.1")
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
