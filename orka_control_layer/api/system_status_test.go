package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

func TestSystemStatusRequiresAuthentication(t *testing.T) {
	a := &API{}
	c := app.NewContext(0)
	a.SystemStatus(context.Background(), c)
	if c.Response.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("status=%d", c.Response.StatusCode())
	}
}
func TestSystemStatusUnconfiguredIsNotReady(t *testing.T) {
	t.Setenv("TOOLS_MCP_URL", "")
	t.Setenv("GUI_AUTH_TOKEN", "")
	a := &API{}
	c := app.NewContext(0)
	c.Set("email", "owner")
	a.SystemStatus(context.Background(), c)
	body := string(c.Response.Body())
	if c.Response.StatusCode() != 200 || !strings.Contains(body, `"ready":false`) || !strings.Contains(body, `"version"`) {
		t.Fatalf("response=%s", body)
	}
}
func TestSystemStatusRedactsDependencyErrors(t *testing.T) {
	report := collectReadiness(context.Background(), []readinessCheck{{name: "mongo", configured: true, probe: func(context.Context) error { return errors.New("mongodb://admin:fake-secret@example.invalid/db") }}})
	if report[0].Status != "unavailable" || strings.Contains(report[0].Detail, "fake-secret") {
		t.Fatalf("unsafe report: %+v", report)
	}
}
func TestSystemStatusProbesOnlyProtocolHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		if r.URL.Path == "/mcp" && r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}}}}`))
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()
	if err := probeGUI(context.Background(), strings.Replace(server.URL, "http:", "ws:", 1)+"/api/v1/exec/gui/ws"); err != nil {
		t.Fatal(err)
	}
	if err := probeTools(context.Background(), server.URL+"/mcp"); err != nil {
		t.Fatal(err)
	}
}
func TestSystemStatusDoesNotFollowRedirectsOrAcceptFakeHealth(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusFound} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://example.invalid/secret")
			w.WriteHeader(code)
			w.Write([]byte(`{"message":"hello"}`))
		}))
		if probeGUI(context.Background(), server.URL) == nil {
			t.Error("non-health response accepted")
		}
		if probeTools(context.Background(), server.URL) == nil {
			t.Error("non-MCP response accepted")
		}
		server.Close()
	}
}
