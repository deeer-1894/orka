package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Doubao response nests results under Result.WebResults and offers two text
// fields per hit. Which one is used is not cosmetic: the API's own docs call
// Snippet (~200 chars) "强烈不建议用于大模型场景" and recommend Summary
// (500-1000 chars), because Summary is the part of the page relevant to the
// query rather than a display teaser.
func TestDoubaoSearchPrefersSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Result":{"ResultCount":2,"WebResults":[
			{"Title":"有摘要的结果","Url":"https://a.example","Snippet":"短的展示片段","Summary":"这是与查询相关的长摘要"},
			{"Title":"只有片段的结果","Url":"https://b.example","Snippet":"回退用的片段","Summary":""}
		]}}`))
	}))
	defer srv.Close()

	t.Setenv("DOUBAO_SEARCH_API_KEY", "test-key")
	out := doubaoSearchAt(context.Background(), srv.URL, "查询", 10)

	if !strings.Contains(out, "这是与查询相关的长摘要") {
		t.Errorf("Summary was not used:\n%s", out)
	}
	if strings.Contains(out, "短的展示片段") {
		t.Errorf("Snippet was used even though Summary was present:\n%s", out)
	}
	// An empty Summary must fall back rather than yielding a blank result.
	if !strings.Contains(out, "回退用的片段") {
		t.Errorf("empty Summary did not fall back to Snippet:\n%s", out)
	}
	for _, want := range []string{"有摘要的结果", "https://a.example", "doubao"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// A provider that fails must return "" so web_search falls through to the next
// one, rather than surfacing a broken result as if it were an answer.
func TestDoubaoSearchFallsThroughOnFailure(t *testing.T) {
	t.Setenv("DOUBAO_SEARCH_API_KEY", "test-key")

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"AccessDenied"}},"Result":null}`))
	}))
	defer bad.Close()
	if got := doubaoSearchAt(context.Background(), bad.URL, "q", 10); got != "" {
		t.Errorf("an error response should yield \"\", got %q", got)
	}

	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer garbage.Close()
	if got := doubaoSearchAt(context.Background(), garbage.URL, "q", 10); got != "" {
		t.Errorf("unparseable body should yield \"\", got %q", got)
	}
}

// searchProviders is what decides whether the keyless fallback is used at all;
// a blank key must read as "not configured" so an empty compose passthrough
// does not register a provider that can only fail.
func TestSearchProvidersSkipsBlankKeys(t *testing.T) {
	t.Setenv("DOUBAO_SEARCH_API_KEY", "")
	t.Setenv("SERPER_API_KEY", "")
	t.Setenv("BRAVE_API_KEY", "")
	t.Setenv("SEARXNG_URL", "")
	if got := len(searchProviders()); got != 0 {
		t.Fatalf("blank keys registered %d providers, want 0", got)
	}
	t.Setenv("DOUBAO_SEARCH_API_KEY", "k")
	if got := len(searchProviders()); got != 1 {
		t.Fatalf("one configured key registered %d providers, want 1", got)
	}
}
