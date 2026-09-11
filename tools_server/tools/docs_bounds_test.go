package tools

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDocsNumericArguments(t *testing.T) {
	s := docsFixture(t, map[string]string{"/llms.txt": "[Retry](/retry)", "/guide": "<h1>Retry</h1><p>Retry details</p>"})
	for _, tc := range []struct {
		name, key, url string
		max            int
	}{
		{"discover_docs", "limit", s.URL, 20}, {"read_section", "max_chars", s.URL + "/guide", 20000},
	} {
		for _, value := range []any{0, -1, 1.5, tc.max + 1, "bogus", false, nil} {
			t.Run(fmt.Sprintf("%s/%v", tc.key, value), func(t *testing.T) {
				res, out := callDocsTool(t, tc.name, map[string]any{"url": tc.url, "query": "retry", tc.key: value})
				if !res.IsError || !strings.Contains(out, tc.key) {
					t.Fatalf("invalid argument accepted: %q", out)
				}
			})
		}
	}
}

func TestDiscoverDocsHTMLLinkEntities(t *testing.T) {
	s := docsFixture(t, map[string]string{"/index.html": `<a href="/guide?a=1&amp;b=2">Retry</a>`})
	got := discoveryResult(t, s.URL+"/index.html", "retry", 2)
	if len(got.Results) != 1 || got.Results[0].URL != s.URL+"/guide?a=1&b=2" {
		t.Fatalf("escaped URL: %+v", got)
	}
}

func TestReadSectionMultipleArticles(t *testing.T) {
	s := docsFixture(t, map[string]string{"/guide": "<article><h1>Introduction</h1><p>Intro only</p></article><article><h2>Retries</h2><p>Use backoff for transient errors.</p></article>"})
	res, out := callDocsTool(t, "read_section", map[string]any{"url": s.URL + "/guide", "query": "backoff"})
	if res.IsError || !strings.Contains(out, "Use backoff") {
		t.Fatalf("discarded later article: %s", out)
	}
}

func TestDocsRejectOversizedBodies(t *testing.T) {
	s := docsFixture(t, map[string]string{"/llms.txt": "[Retry](/retry)\n" + strings.Repeat("x", (2<<20)+1), "/guide": "<h1>Retry</h1>" + strings.Repeat("x", (2<<20)+1)})
	for _, name := range []string{"discover_docs", "read_section", "fetch_url"} {
		u := s.URL + "/guide"
		if name == "discover_docs" {
			u = s.URL
		}
		res, out := callDocsTool(t, name, map[string]any{"url": u, "query": "retry"})
		if !res.IsError || !strings.Contains(out, "response body exceeds") {
			t.Fatalf("%s accepted oversized response: %.100s", name, out)
		}
	}
}

func TestDiscoverDocsMalformedXML(t *testing.T) {
	s := docsFixture(t, map[string]string{"/index.xml": "<urlset><url><loc>/retry</loc></url>"})
	res, out := callDocsTool(t, "discover_docs", map[string]any{"url": s.URL + "/index.xml"})
	if !res.IsError || !strings.Contains(out, "invalid sitemap XML") {
		t.Fatalf("accepted malformed sitemap: %s", out)
	}
}

func TestDiscoverDocsStopsNestedSitemapIndexes(t *testing.T) {
	s := docsFixture(t, map[string]string{
		"/sitemap.xml":    `<sitemapindex><sitemap><loc>/child.xml</loc></sitemap></sitemapindex>`,
		"/child.xml":      `<sitemapindex><sitemap><loc>/grandchild.xml</loc></sitemap></sitemapindex>`,
		"/grandchild.xml": `<urlset><url><loc>/retry</loc></url></urlset>`,
	})
	res, out := callDocsTool(t, "discover_docs", map[string]any{"url": s.URL})
	if !res.IsError || !strings.Contains(out, "traversal bounds") {
		t.Fatalf("followed grandchild index: %s", out)
	}
}

func TestDiscoverDocsTitleBound(t *testing.T) {
	s := docsFixture(t, map[string]string{"/llms.txt": "[" + strings.Repeat("中文🙂", 300) + "](/retry)"})
	got := discoveryResult(t, s.URL, "retry", 1)
	if len(got.Results) != 1 || !utf8.ValidString(got.Results[0].Title) || utf8.RuneCountInString(got.Results[0].Title) > 200 {
		t.Fatalf("unbounded title: %+v", got)
	}
}

func TestDocsHTTPStatuses(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "NOT_PAGE_CONTENT", http.StatusServiceUnavailable)
	}))
	defer s.Close()
	for _, name := range []string{"discover_docs", "read_section", "fetch_url"} {
		res, out := callDocsTool(t, name, map[string]any{"url": s.URL, "query": "retry"})
		if !res.IsError || !strings.Contains(out, "HTTP 503") || strings.Contains(out, "NOT_PAGE_CONTENT") {
			t.Errorf("%s returned HTTP error body: %s", name, out)
		}
	}
}
