package connectoroauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
)

type memStore struct {
	mu    sync.Mutex
	conns map[string]*db.Connector
}

func (s *memStore) CreateConnector(_ context.Context, c *db.Connector) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *c
	s.conns[c.ConnectorID] = &cp
	return nil
}

func (s *memStore) SaveConnectorOAuth(_ context.Context, id, owner string, g *db.ConnectorOAuth) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.conns[id]; c != nil && c.OwnerEmail == owner {
		cp := *g
		c.OAuth = &cp
	}
	return nil
}

// fakeAuthServer is an MCP server that follows MCP authorization: 401 with
// resource metadata, an authorization server with dynamic registration, and a
// token endpoint that checks PKCE and the resource indicator.
type fakeAuthServer struct {
	*httptest.Server
	challenge   string
	issued      int
	failRefresh bool
}

func newFakeAuthServer(t *testing.T) *fakeAuthServer {
	f := &fakeAuthServer{}
	mux := http.NewServeMux()
	f.Server = httptest.NewTLSServer(mux)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.URL+`/.well-known/oauth-protected-resource/mcp"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"resource": f.URL + "/mcp", "authorization_servers": []string{f.URL + "/auth"}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server/auth", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": f.URL + "/auth/authorize", "token_endpoint": f.URL + "/auth/token", "registration_endpoint": f.URL + "/auth/register"})
	})
	mux.HandleFunc("/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["token_endpoint_auth_method"] != "none" {
			t.Errorf("registration must be for a public PKCE client: %v", body)
		}
		json.NewEncoder(w).Encode(map[string]any{"client_id": "client-1"})
	})
	mux.HandleFunc("/auth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("resource") != f.URL+"/mcp" || r.Form.Get("client_id") != "client-1" {
			t.Errorf("token request lacks resource or client: %v", r.Form)
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "code-1" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
				return
			}
		case "refresh_token":
			if f.failRefresh || r.Form.Get("refresh_token") != "refresh-1" {
				json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
				return
			}
		}
		f.issued++
		json.NewEncoder(w).Encode(map[string]any{"access_token": "access-" + string(rune('0'+f.issued)), "refresh_token": "refresh-1", "expires_in": 60, "token_type": "Bearer"})
	})
	t.Cleanup(f.Close)
	return f
}

func TestMCPAuthorizationFlowStoresSealedGrantAndRefreshes(t *testing.T) {
	srv := newFakeAuthServer(t)
	sealer, _ := NewSealer("test-secret")
	store := &memStore{conns: map[string]*db.Connector{}}
	m := &Manager{Store: store, Sealer: sealer, Client: srv.Client()}
	ctx := context.Background()

	link, err := m.Start(ctx, StartRequest{Owner: "a@x", Provider: ProviderMCP, MCPURL: srv.URL + "/mcp", Redirect: "http://localhost:5173" + "/cb"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link)
	q := u.Query()
	if !strings.HasPrefix(link, srv.URL+"/auth/authorize?") || q.Get("code_challenge_method") != "S256" || q.Get("resource") != srv.URL+"/mcp" || q.Get("client_id") != "client-1" {
		t.Fatalf("authorize url = %s", link)
	}
	srv.challenge = q.Get("code_challenge")

	cn, err := m.Complete(ctx, q.Get("state"), "code-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Complete(ctx, q.Get("state"), "code-1"); err == nil {
		t.Fatal("a state is single-use")
	}
	stored := store.conns[cn.ConnectorID]
	if stored == nil || stored.OwnerEmail != "a@x" || stored.URL != srv.URL+"/mcp" || stored.OAuth == nil {
		t.Fatalf("stored connector %+v", stored)
	}
	if strings.Contains(stored.OAuth.AccessToken, "access-") || strings.Contains(stored.OAuth.RefreshToken, "refresh-") {
		t.Fatal("tokens must be stored sealed")
	}
	if b, _ := json.Marshal(stored); strings.Contains(string(b), stored.OAuth.AccessToken) {
		t.Fatal("sealed tokens must never be serialized to clients")
	}

	// 60s tokens are inside the refresh leeway: Authorize refreshes first.
	h, err := m.Authorize(ctx, *stored)
	if err != nil || h["Authorization"] != "Bearer access-2" {
		t.Fatalf("headers %v err %v, want the refreshed token", h, err)
	}

	srv.failRefresh = true
	m.Now = func() time.Time { return time.Now().Add(time.Hour) }
	if _, err := m.Authorize(ctx, *store.conns[cn.ConnectorID]); err == nil {
		t.Fatal("a failed refresh must fail the request")
	}
	if store.conns[cn.ConnectorID].OAuth.AuthError == "" {
		t.Fatal("a failed refresh marks the grant for re-authorization")
	}
}

func TestStartRefusesWhatCannotWork(t *testing.T) {
	m := &Manager{Client: http.DefaultClient}
	if _, err := m.Start(context.Background(), StartRequest{Provider: ProviderMCP, MCPURL: "http://insecure.example/mcp", Redirect: "http://x/cb"}); err == nil {
		t.Fatal("a generic MCP server must be https")
	}
	t.Setenv("ORKA_GITHUB_CLIENT_ID", "")
	if _, err := m.Start(context.Background(), StartRequest{Provider: "github", Redirect: "http://x/cb"}); err == nil || !strings.Contains(err.Error(), "部署方") {
		t.Fatalf("an unconfigured preset says what is missing, got %v", err)
	}
	if _, err := m.Complete(context.Background(), "never-issued", "code"); err == nil {
		t.Fatal("an unknown state is refused")
	}
}

func TestSealerRoundTripAndRefusesPlaintext(t *testing.T) {
	s, _ := NewSealer("k")
	sealed, _ := s.Seal("token")
	if got, err := s.Open(sealed); err != nil || got != "token" {
		t.Fatalf("round trip: %q %v", got, err)
	}
	if _, err := s.Open("token"); err == nil {
		t.Fatal("plaintext is not a sealed secret")
	}
	other, _ := NewSealer("other")
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("another key cannot open it")
	}
	if _, err := NewSealer(" "); err == nil {
		t.Fatal("an empty key is refused")
	}
}
