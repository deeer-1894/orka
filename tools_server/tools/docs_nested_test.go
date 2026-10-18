package tools

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Small invented catalogs reproduce the shape of multilingual/version indexes;
// no downloaded documentation content is embedded in these fixtures.
func TestDiscoverNestedLLMSPrefersLanguage(t *testing.T) {
	for _, tc := range []struct{ input, want, first string }{
		{"/", "/en/retry", "/_llms/en.md"},
		{"/zh/", "/zh/retry", "/_llms/zh.md"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			var mu sync.Mutex
			var requests []string
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.URL.Path)
				mu.Unlock()
				switch r.URL.Path {
				case "/llms.txt", "/zh/llms.txt":
					fmt.Fprint(w, "# Catalogs\n[Arabic](/_llms/ar.md)\n[Arabic / v1](/_llms/ar/v1.md)\n[English / v1](/_llms/en/v1.md)\n[English](/_llms/en.md)\n[Chinese](/_llms/zh.md)")
				case "/_llms/en.md", "/_llms/en/v1.md":
					fmt.Fprint(w, "[Retry guide](/en/retry)")
				case "/_llms/zh.md":
					fmt.Fprint(w, "[重试指南](/zh/retry)")
				default:
					http.NotFound(w, r)
				}
			}))
			defer s.Close()
			got := discoveryResult(t, s.URL+tc.input, "", 10)
			if len(got.Results) == 0 || got.Results[0].URL != s.URL+tc.want || got.Results[0].Kind != "page" {
				t.Fatalf("catalogs returned instead of preferred docs: %+v", got)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) > 5 || len(requests) < 2 || requests[1] != tc.first {
				t.Errorf("wrong expansion order/budget: %v", requests)
			}
			for _, u := range requests {
				if strings.HasPrefix(u, "/_llms/ar") {
					t.Errorf("spent preferred-language budget on %s", u)
				}
			}
		})
	}
}

func TestDiscoverNestedLLMSStopsAtOneLevel(t *testing.T) {
	s := docsFixture(t, map[string]string{
		"/llms.txt":       "[English](/_llms/en.md)",
		"/_llms/en.md":    "[Version catalog](/_llms/en/v2.md)",
		"/_llms/en/v2.md": "[Should not reach](/deep-page)",
	})
	got := discoveryResult(t, s.URL, "", 10)
	if len(got.Results) != 1 || got.Results[0].URL != s.URL+"/_llms/en/v2.md" || got.Results[0].Kind != "index" {
		t.Fatalf("recursed or mislabeled unexpanded index: %+v", got)
	}
}

func TestDiscoverNestedLLMSBoundAndLabels(t *testing.T) {
	var count int
	var mu sync.Mutex
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		if r.URL.Path == "/llms.txt" {
			for i := 0; i < 12; i++ {
				fmt.Fprintf(w, "[English / v%d](/_llms/en/v%02d.md)\n", i, i)
			}
			return
		}
		fmt.Fprint(w, "[Document](/en/retry)")
	}))
	defer s.Close()
	got := discoveryResult(t, s.URL, "", 20)
	indexes := 0
	for _, result := range got.Results {
		if strings.Contains(result.URL, "/_llms/") {
			if result.Kind != "index" {
				t.Errorf("unexpanded catalog presented as document: %+v", result)
			}
			indexes++
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if count != 5 || indexes != 8 || got.Results[0].Kind != "page" {
		t.Fatalf("unexpected bound/labels: requests=%d indexes=%d results=%+v", count, indexes, got)
	}
}

func TestDiscoverNestedLLMSRejectsOffOriginAndCycles(t *testing.T) {
	s := docsFixture(t, map[string]string{"/llms.txt": "[Loop](/llms.txt)\n[Other](https://elsewhere.invalid/_llms/en.md)"})
	res, out := callDocsTool(t, "discover_docs", map[string]any{"url": s.URL})
	if !res.IsError {
		t.Fatalf("cycle or external index reported as useful discovery: %s", out)
	}
}
