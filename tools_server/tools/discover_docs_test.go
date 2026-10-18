package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type discoveredDocs struct {
	IndexSource string `json:"index_source"`
	Results     []struct {
		Title string `json:"title"`
		Kind  string `json:"kind"`
		URL   string `json:"url"`
	} `json:"results"`
}

func discoveryResult(t *testing.T, url, query string, limit int) discoveredDocs {
	t.Helper()
	res, out := callDocsTool(t, "discover_docs", map[string]any{"url": url, "query": query, "limit": limit})
	if res.IsError {
		t.Fatalf("discovery failed: %s", out)
	}
	var got discoveredDocs
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDiscoverDocsMarkdownRanking(t *testing.T) {
	s := docsFixture(t, map[string]string{"/llms.txt": `# Official docs
- [Intro](/intro): Overview
- [Retries](/retry): retry policy BODY_NOT_RETURNED
- [Retry advanced](/retry-advanced)
- [Duplicate](/retry)
- [Other](https://elsewhere.invalid/retry)
- [Unsafe](javascript:alert)
`})
	got := discoveryResult(t, s.URL, "retry", 2)
	if got.IndexSource != s.URL+"/llms.txt" || len(got.Results) != 2 {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got.Results[0].URL != s.URL+"/retry-advanced" || got.Results[1].URL != s.URL+"/retry" {
		t.Fatalf("ranking/dedup: %+v", got)
	}
	again := discoveryResult(t, s.URL, "retry", 2)
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(again)
	if string(a) != string(b) || strings.Contains(string(a), "BODY_NOT_RETURNED") {
		t.Fatalf("unstable or verbose results: %s", a)
	}
}

func TestDiscoverDocsSitemapFallback(t *testing.T) {
	s := docsFixture(t, map[string]string{"/llms.txt": "<html>SPA shell</html>", "/sitemap.xml": `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>/intro</loc></url><url><loc>/docs/retry-policy?a=1&amp;b=2</loc></url></urlset>`})
	got := discoveryResult(t, s.URL, "retry", 1)
	if got.IndexSource != s.URL+"/sitemap.xml" || len(got.Results) != 1 || got.Results[0].URL != s.URL+"/docs/retry-policy?a=1&b=2" || got.Results[0].Title == "" {
		t.Fatalf("unexpected sitemap results: %+v", got)
	}
}

func TestDiscoverDocsSuppliedIndexes(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/catalog.md", "# Docs\n[Retry](guide/retry.md)"},
		{"/catalog.html", `<html><a href="guide/retry.md">Retry &amp; limits</a></html>`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s := docsFixture(t, map[string]string{tc.path: tc.body})
			got := discoveryResult(t, s.URL+tc.path, "retry", 5)
			if got.IndexSource != s.URL+tc.path || len(got.Results) != 1 || got.Results[0].URL != s.URL+"/guide/retry.md" {
				t.Fatalf("bad supplied index: %+v", got)
			}
		})
	}
}

func TestDiscoverDocsNoIndex(t *testing.T) {
	s := docsFixture(t, nil)
	res, out := callDocsTool(t, "discover_docs", map[string]any{"url": s.URL})
	if !res.IsError || !strings.Contains(out, "no usable documentation index") || !strings.Contains(out, "HTTP 404") {
		t.Fatalf("missing actionable error: %q", out)
	}
}

func TestDiscoverDocsBoundedTraversal(t *testing.T) {
	var requests, external atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { external.Add(1) }))
	defer other.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/llms.txt" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/sitemap.xml" {
			fmt.Fprintf(w, `<sitemapindex><sitemap><loc>%s/escape.xml</loc></sitemap>`, other.URL)
			for i := 0; i < 100; i++ {
				fmt.Fprintf(w, `<sitemap><loc>/child-%03d.xml</loc></sitemap>`, i)
			}
			fmt.Fprint(w, `</sitemapindex>`)
			return
		}
		fmt.Fprint(w, `<urlset><url><loc>/retry</loc></url></urlset>`)
	}))
	defer s.Close()
	got := discoveryResult(t, s.URL, "retry", 20)
	if len(got.Results) != 1 || requests.Load() > 5 || external.Load() != 0 {
		t.Fatalf("traversal exceeded bounds: requests=%d external=%d result=%+v", requests.Load(), external.Load(), got)
	}
}

func TestDiscoverDocsRejectsCrossOriginRedirect(t *testing.T) {
	var requests atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer other.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	defer s.Close()
	res, _ := callDocsTool(t, "discover_docs", map[string]any{"url": s.URL})
	if !res.IsError || requests.Load() != 0 {
		t.Fatalf("followed off-site index redirect: %d", requests.Load())
	}
}
