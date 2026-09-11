package tools

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func sectionBody(t *testing.T, out string) string {
	t.Helper()
	headers, body, ok := strings.Cut(out, "\n\n")
	if !ok || !strings.HasPrefix(headers, "URL: ") || !strings.Contains(headers, "\nTitle: ") {
		t.Fatalf("missing source metadata: %.300s", out)
	}
	return body
}

func TestMarkdownCodeDoesNotOverridePageType(t *testing.T) {
	markdown := "# Markdown guide\n\n```html\n<main>Hello</main>\n```\n\n## Retry configuration\nUse jitter-setting for backoff."
	for _, contentType := range []string{"text/markdown; charset=utf-8", "text/x-markdown", "text/plain"} {
		t.Run(contentType, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", contentType)
				_, _ = w.Write([]byte(markdown))
			}))
			defer s.Close()
			for _, name := range []string{"fetch_url", "read_section"} {
				res, out := callDocsTool(t, name, map[string]any{"url": s.URL + "/guide", "query": "jitter-setting"})
				if res.IsError || !strings.Contains(out, "Use jitter-setting for backoff") {
					t.Errorf("%s lost Markdown content: %s", name, out)
				}
			}
		})
	}
}

func TestReadSectionRanksPassagesByQueryCoverage(t *testing.T) {
	for _, heading := range []string{"<h1>Retry configuration</h1>", ""} {
		s := docsFixture(t, map[string]string{"/guide": heading + "<p>Retry basics. " + strings.Repeat("General introduction. ", 2000) + "Use retry jitter-setting for bounded delays. " + strings.Repeat("Other material. ", 1000) + "</p>"})
		res, out := callDocsTool(t, "read_section", map[string]any{"url": s.URL + "/guide", "query": "retry jitter-setting", "max_chars": 180})
		if res.IsError || !strings.Contains(out, "retry jitter-setting for bounded delays") {
			t.Fatalf("earliest generic match hid specific passage: %s", out)
		}
		if heading != "" && !strings.Contains(out, "## Retry configuration") {
			t.Errorf("lost section heading: %s", out)
		}
		if body := sectionBody(t, out); utf8.RuneCountInString(body) > 180 {
			t.Errorf("excerpt exceeds budget: %q", body)
		}
	}
}

func TestReadSectionChinesePunctuation(t *testing.T) {
	s := docsFixture(t, map[string]string{"/guide": "<h1>错误处理</h1><p>发生超时时可以重试。</p>", "/llms.txt": "[简介](/intro)\n[重试与超时](/errors)"})
	res, out := callDocsTool(t, "read_section", map[string]any{"url": s.URL + "/guide", "query": "重试、超时"})
	if res.IsError || !strings.Contains(out, "发生超时时可以重试") {
		t.Fatalf("punctuation blocked Chinese query: %s", out)
	}
	got := discoveryResult(t, s.URL, "重试、超时", 1)
	if got.Results[0].URL != s.URL+"/errors" {
		t.Fatalf("punctuation broke discovery ranking: %+v", got)
	}
}

func TestQueryTermsPreserveIdentifiers(t *testing.T) {
	got := queryTerms("重试、超时，retry-count；retry_count。重试 / Retry-Count")
	want := []string{"重试", "超时", "retry-count", "retry_count"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("query terms = %#v, want %#v", got, want)
	}
}

func TestPageToolsReturnResolvedSourceMetadata(t *testing.T) {
	for _, tc := range []struct{ name, contentType, body, title string }{
		{"html", "text/html", "<title>Canonical &amp; Guide</title><h1>Retry</h1><p>Retry details here.</p>", "Canonical & Guide"},
		{"markdown", "text/markdown", "```sh\n# Fake title\n```\n# Markdown guide\n## Retry\nRetry details here.", "Markdown guide"},
		{"untitled", "text/plain", "Retry details here.", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/old" {
					http.Redirect(w, r, "/canonical?version=2", http.StatusFound)
					return
				}
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			for _, name := range []string{"read_section", "fetch_url"} {
				res, out := callDocsTool(t, name, map[string]any{"url": s.URL + "/old", "query": "retry", "max_chars": 1})
				expected := "URL: " + s.URL + "/canonical?version=2\nTitle: " + tc.title + "\n\n"
				if res.IsError || !strings.HasPrefix(out, expected) {
					t.Errorf("%s wrong source headers: %q, want prefix %q", name, out, expected)
					continue
				}
				if name == "read_section" && utf8.RuneCountInString(sectionBody(t, out)) > 1 {
					t.Errorf("excerpt exceeds 1 character: %q", out)
				}
			}
		})
	}
}
