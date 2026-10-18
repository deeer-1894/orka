package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/orka-oss/tools_server/identity"
)

func TestDiscoverDocumentationDirectoryBeforeRoot(t *testing.T) {
	for _, u := range []string{"/autogen/", "/autogen"} {
		t.Run(u, func(t *testing.T) {
			s := docsFixture(t, map[string]string{"/autogen/llms.txt": "[Project docs](/autogen/guide)", "/llms.txt": "[Other project](/other/guide)"})
			got := discoveryResult(t, s.URL+u, "", 1)
			if got.IndexSource != s.URL+"/autogen/llms.txt" || got.Results[0].URL != s.URL+"/autogen/guide" {
				t.Fatalf("ignored documentation base: %+v", got)
			}
		})
	}
}

func TestDiscoverDirectoryFallsBackToRoot(t *testing.T) {
	s := docsFixture(t, map[string]string{"/llms.txt": "[Root docs](/guide)"})
	got := discoveryResult(t, s.URL+"/autogen/", "", 1)
	if got.IndexSource != s.URL+"/llms.txt" || got.Results[0].URL != s.URL+"/guide" {
		t.Fatalf("lost root fallback: %+v", got)
	}
}

func TestDiscoverLandingExpandsOnce(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprint(nested), func(t *testing.T) {
			target := "/autogen/guide"
			kind := "page"
			if nested {
				target = "/autogen/stable/further/"
				kind = "index"
			}
			s := docsFixture(t, map[string]string{
				"/autogen/":                `<html><a href="/autogen/stable/">link to example.com</a></html>`,
				"/autogen/stable/":         `<html><a href="` + target + `">Guide</a></html>`,
				"/autogen/stable/further/": `<html><a href="/too-deep">Too deep</a></html>`,
			})
			got := discoveryResult(t, s.URL+"/autogen/", "", 10)
			if len(got.Results) != 1 || got.Results[0].URL != s.URL+target || got.Results[0].Kind != kind {
				t.Fatalf("landing traversal/label wrong: %+v", got)
			}
		})
	}
}

func TestDiscoverOrdinaryLinksAreNotCrawled(t *testing.T) {
	s := docsFixture(t, map[string]string{"/llms.txt": "[Article](/article/)", "/article/": "[Should not fetch](/hidden)"})
	got := discoveryResult(t, s.URL, "", 10)
	if len(got.Results) != 1 || got.Results[0].URL != s.URL+"/article/" || got.Results[0].Kind != "page" {
		t.Fatalf("crawled ordinary doc link: %+v", got)
	}
}

func TestDiscoverRedirectsShareFiveRequestBudget(t *testing.T) {
	var count int
	var mu sync.Mutex
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		http.Redirect(w, r, r.URL.Path+"/next", http.StatusFound)
	}))
	defer s.Close()
	res, out := callDocsTool(t, "discover_docs", map[string]any{"url": s.URL + "/autogen/"})
	mu.Lock()
	defer mu.Unlock()
	if !res.IsError || count > 5 || !strings.Contains(out, "request budget") {
		t.Fatalf("redirects escaped request budget: requests=%d output=%s", count, out)
	}
}

func TestDiscoverHonorsParentCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	server := mcpserver.NewMCPServer("test", "1")
	Register(server, t.TempDir(), nil)
	ctx, cancel := context.WithTimeout(identity.With(context.Background(), identity.Identity{Scopes: []string{"web:search"}}), 50*time.Millisecond)
	defer cancel()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"url": s.URL}
	start := time.Now()
	res, err := server.GetTool("discover_docs").Handler(ctx, req)
	if err != nil || !res.IsError || time.Since(start) > time.Second {
		t.Fatalf("discovery ignored deadline: %v", err)
	}
}

func TestDiscoverLandingDirectoryWithQuery(t *testing.T) {
	s := docsFixture(t, map[string]string{
		"/autogen/":        `<html><a href="/autogen/stable/?version=2">Stable docs</a></html>`,
		"/autogen/stable/": `<html><a href="/autogen/guide">Guide</a></html>`,
	})
	got := discoveryResult(t, s.URL+"/autogen/", "", 1)
	if got.Results[0].URL != s.URL+"/autogen/guide" || got.Results[0].Kind != "page" {
		t.Fatalf("query hid directory landing link: %+v", got)
	}
}
