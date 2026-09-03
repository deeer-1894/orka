package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A guessed repository path is the failure that recurs — four in one run — and
// the useful reply is the listing, not a scolding. The URL carries org/repo/ref,
// so the tool can name the exact API call that ends the guessing.
func TestFetchFailureHintPointsAtTheRepoTree(t *testing.T) {
	got := fetchFailureHint(context.Background(),
		"https://raw.githubusercontent.com/crewAIInc/crewAI/main/src/crewai/task.py", 404)

	for _, want := range []string{"HTTP 404", "api.github.com/repos/crewAIInc/crewAI/git/trees/main"} {
		if !strings.Contains(got, want) {
			t.Errorf("hint missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "do not guess another one") {
		t.Errorf("hint does not discourage the next guess:\n%s", got)
	}
}

// Doing the convention lookup beats asking the model to remember it.
func TestFetchFailureHintFindsLLMsTxt(t *testing.T) {
	var probed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probed = r.URL.Path
		if r.URL.Path == "/llms.txt" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("- [Page](https://x/page.md)\n"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	got := fetchFailureHint(context.Background(), srv.URL+"/docs/missing", 404)
	if probed != "/llms.txt" {
		t.Errorf("did not probe /llms.txt, probed %q", probed)
	}
	if !strings.Contains(got, "/llms.txt") {
		t.Errorf("hint does not offer the index:\n%s", got)
	}
}

// A site that answers every path with its HTML shell must not be reported as
// having an index — that would send the agent to fetch a page of markup.
func TestProbeLLMsTxtIgnoresHTMLShells(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>SPA</body></html>"))
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL + "/anything")
	if got := probeLLMsTxt(context.Background(), u); got != "" {
		t.Errorf("an HTML shell was reported as an llms.txt index: %q", got)
	}
}

// Without an index and without a repo path, the hint is still an honest failure
// rather than the 404 body dressed up as content.
func TestFetchFailureHintPlainCase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	got := fetchFailureHint(context.Background(), srv.URL+"/gone", 404)
	if !strings.HasPrefix(got, "fetch failed: HTTP 404") {
		t.Errorf("failure not reported as a failure: %s", got)
	}
	if strings.Contains(got, "llms.txt") {
		t.Errorf("offered an index that does not exist: %s", got)
	}
}
