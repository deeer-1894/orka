package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// fetchFailureHint reports a failed fetch AND points somewhere useful, because
// telling a model only that a URL is missing invites it to guess the next one.
//
// Two leads, both cheap. Many documentation sites publish a machine-readable
// index of every page at /llms.txt — checked here rather than left to the
// prompt, since asking the model to remember a convention is worth roughly half
// the compliance of doing it for them (measured across this codebase's prompt
// fixes). And a path guessed inside a source repository is the failure mode that
// actually recurs, so that case names the API that lists the real tree.
func fetchFailureHint(ctx context.Context, u string, status int) string {
	msg := fmt.Sprintf("fetch failed: HTTP %d for %s", status, u)
	parsed, err := url.Parse(u)
	if err != nil {
		return msg
	}
	if status == http.StatusNotFound && parsed.Host == "raw.githubusercontent.com" {
		// /<org>/<repo>/<ref>/<path...>
		if p := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 4); len(p) >= 3 {
			msg += fmt.Sprintf(". That path does not exist — do not guess another one."+
				" List the real tree first: https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1", p[0], p[1], p[2])
			return msg
		}
	}
	if idx := probeLLMsTxt(ctx, parsed); idx != "" {
		msg += ". This site publishes an index of its pages for machine use at " + idx + " — fetch that to find the real URL"
	}
	return msg
}

// probeLLMsTxt returns the site's llms.txt URL when it exists, else "". One GET
// on a path that is already failing, so it costs nothing on the happy path.
func probeLLMsTxt(ctx context.Context, u *url.URL) string {
	cand := u.Scheme + "://" + u.Host + "/llms.txt"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cand, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; OrkaBot/0.1)")
	resp, err := httpSearchC.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return ""
	}
	// A site that serves its SPA shell for every unknown path would otherwise
	// look like it has one; llms.txt is plain text by construction.
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "text/plain") && !strings.Contains(ct, "text/markdown") {
		return ""
	}
	return cand
}

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
		hreq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; OrkaBot/0.1)")
		resp, err := httpSearchC.Do(hreq)
		if err != nil {
			return mcp.NewToolResultError("fetch failed: " + err.Error()), nil
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		// A 404 body was being returned as if it were the page. The agent then saw
		// a "result" whose text was "404: Not Found" and kept guessing neighbouring
		// paths, because nothing had told it the fetch FAILED — four of a run's six
		// fetch failures were guessed repository paths in a row. Say so, and hand
		// back a lead instead of a dead end.
		if resp.StatusCode/100 != 2 {
			return mcp.NewToolResultError(fetchFailureHint(ctx, u, resp.StatusCode)), nil
		}
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
