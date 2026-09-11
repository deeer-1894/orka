package tools

import (
	"context"
	"errors"
	"fmt"
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
	// Site chrome. Stripping only script/style left every documentation page
	// carrying its own sidebar and footer into the model's context: one fetched
	// CloudWeGo page began "Title: Summarization | CloudWeGo DocumentationKitex
	// Hertz Volo EinoAboutBlogCooperation…" followed by the site's whole article
	// list before a word of the page itself. Downstream that boilerplate is what
	// the context layer's placeholder budget got spent describing, so the model
	// re-read the archive to find the content — the cheapest place to fix it is
	// here, before it is ever stored.
	//
	// Non-greedy and unanchored on purpose: nested <nav> would leave a stray
	// close tag, which the tag stripper removes anyway.
	reChrome = regexp.MustCompile(`(?is)<(nav|header|footer|aside)[^>]*>.*?</(nav|header|footer|aside)>`)
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

// Keep the legacy 20,000-byte preview budget below the control layer's output
// threshold. Truncate at a UTF-8 boundary; read_section can retrieve matching
// content beyond this preview without returning an entire page.
const maxFetchBodyChars = 20000

// fetchURL downloads a page and returns its readable text. Pairs with
// web_search: search to find a link, fetch_url to read it — no GUI needed.
func fetchURL() mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parsed, err := parsePageURL(req.GetString("url", ""), true)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		page, err := loadPage(ctx, parsed, nil, maxPageBytes)
		if err != nil {
			var status *pageStatusError
			if errors.As(err, &status) {
				return mcp.NewToolResultError(fetchFailureHint(ctx, status.URL, status.Status)), nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}
		readable := extractPage(page)
		body := truncatePageBytes(readable.Text, maxFetchBodyChars)
		out := formatPageText(page.URL.String(), readable.Title, body)
		return mcp.NewToolResultText(out), nil
	}
}
