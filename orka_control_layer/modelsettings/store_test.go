package modelsettings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrivatePersistenceAndKeyBinding(t *testing.T) {
	base := filepath.Join(t.TempDir(), "storage")
	s := New(base)
	c := Config{Provider: "custom", BaseURL: "http://localhost:1234/v1/", Model: "manual", MiniModel: "fast", Models: []string{"extra", "extra"}, Enabled: true}
	saved, err := s.Save("alice@example.com", c, "private-key")
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Public().APIKeySet {
		t.Fatal("missing key flag")
	}
	b, _ := json.Marshal(saved.Public())
	if strings.Contains(string(b), "private-key") || strings.Contains(string(b), `"api_key"`) {
		t.Fatal(string(b))
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(base), "model-settings", "*.json"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	st, _ := os.Stat(files[0])
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	if strings.Contains(filepath.Base(files[0]), "alice") {
		t.Fatal("owner not hashed")
	}
	reread, _, err := New(base).Get("alice@example.com")
	if err != nil || reread.APIKey != "private-key" {
		t.Fatalf("restart: %v", err)
	}
	_, exists, err := s.Get("bob@example.com")
	if exists || err != nil {
		t.Fatal("cross user leak", err)
	}
	saved, err = s.Save("alice@example.com", c, " ")
	if err != nil || saved.APIKey != "private-key" {
		t.Fatal("blank lost key", err)
	}
	c.BaseURL = "http://localhost:4567/v1"
	saved, err = s.Save("alice@example.com", c, "")
	if err != nil || saved.APIKey != "" {
		t.Fatal("key forwarded to changed URL", err)
	}
	saved, err = s.Save("alice@example.com", c, "replacement")
	if err != nil || saved.APIKey != "replacement" {
		t.Fatal(err)
	}
	saved, err = s.Save("alice@example.com", Config{Enabled: false}, "")
	if err != nil || saved.Enabled || saved.APIKey != "replacement" {
		t.Fatal("disable", err)
	}
}

func TestURLValidation(t *testing.T) {
	for _, u := range []string{"ftp://host", "http://user:pass@host/v1", "https://host?q=key", "https://host#fragment", "https://host?", "https://host#", "http:///v1", "//host/v1"} {
		if _, err := NormalizeURL(u); err == nil {
			t.Errorf("accepted %q", u)
		}
	}
	for _, u := range []string{"http://localhost:11434/v1", "http://[::1]:8080/v1", "https://openrouter.ai/api/v1", "https://api.example.com/compatible-mode/v1"} {
		if _, err := NormalizeURL(u); err != nil {
			t.Errorf("rejected %q: %v", u, err)
		}
	}
}

func TestDiscoverBoundedAndKeyScoped(t *testing.T) {
	s := New(t.TempDir())
	var auth, path string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		path = r.URL.Path
		w.Write([]byte(`{"data":[{"id":"z"},{"id":"a"},{"id":"z"},{"id":""}]}`))
	}))
	defer upstream.Close()
	c := Config{BaseURL: upstream.URL + "/v1", Model: "manual", Enabled: true}
	if _, err := s.Save("alice", c, "secret"); err != nil {
		t.Fatal(err)
	}
	models, err := s.Discover(context.Background(), "alice", c.BaseURL, "")
	if err != nil || strings.Join(models, ",") != "a,z" || auth != "Bearer secret" || path != "/v1/models" {
		t.Fatal(models, err, auth, path)
	}
	_, err = s.Discover(context.Background(), "bob", c.BaseURL, "")
	if err != nil || auth != "" {
		t.Fatal("other owner key", err, auth)
	}
	_, err = s.Discover(context.Background(), "alice", upstream.URL+"/other", "")
	if err != nil || auth != "" {
		t.Fatal("changed base key", err, auth)
	}
	_, err = s.Discover(context.Background(), "alice", upstream.URL+"/other", "new")
	if err != nil || auth != "Bearer new" {
		t.Fatal("new key", err, auth)
	}
}

func TestDiscoverRejectsRedirectOversizeMalformedAndTimeout(t *testing.T) {
	s := New(t.TempDir())
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	if _, err := s.Discover(context.Background(), "alice", redirect.URL, "secret"); err == nil || reached {
		t.Fatal("redirect followed")
	}
	for _, body := range []string{strings.Repeat("x", maxResponseBytes+1), `{"error":{"message":"secret"}}`, `{"data":null}`, `garbage secret`} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		_, err := s.Discover(context.Background(), "alice", ts.URL, "secret")
		ts.Close()
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe discovery error", err)
		}
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Discover(ctx, "alice", slow.URL, ""); err == nil {
		t.Fatal("timeout missing")
	}
}

func TestRejectSymlinkStorage(t *testing.T) {
	base := filepath.Join(t.TempDir(), "storage")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(base), "model-settings")); err != nil {
		t.Fatal(err)
	}
	if _, err := New(base).Save("alice", Config{BaseURL: "http://localhost/v1", Model: "m", Enabled: true}, "secret"); err == nil {
		t.Fatal("followed storage symlink")
	}
}

func TestLegacyConfigMigratesToOrderedList(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"drop standalone mini", `{"model":"original","mini_model":"fast","models":["manual","original"],"enabled":true}`, "original,manual"},
		{"keep listed mini as ordinary", `{"model":"original","mini_model":"fast","models":["fast","manual"],"enabled":true}`, "original,fast,manual"},
		{"list only", `{"models":["second","first","second"],"enabled":true}`, "second,first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(filepath.Join(t.TempDir(), "storage"))
			if err := s.directories(true); err != nil {
				t.Fatal(err)
			}
			path, _ := s.path("owner")
			body := strings.TrimSuffix(tc.body, "}") + `,"base_url":"http://localhost/v1","api_key":"private-key"}`
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			c, _, err := s.Get("owner")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(c.Models, ",") != tc.want || c.APIKey != "private-key" {
				t.Fatal("bad migration", c.Models)
			}
			pub, _ := json.Marshal(c.Public())
			if strings.Contains(string(pub), `"model":`) || strings.Contains(string(pub), `"mini_model":`) {
				t.Fatal(string(pub))
			}
			// Reordering a migrated list must not reinsert the old default on save.
			c.Models = []string{"new-first", "original"}
			c, err = s.Save("owner", c, "")
			if err != nil || c.Models[0] != "new-first" || c.APIKey != "private-key" {
				t.Fatal("reorder failed", err)
			}
			disk, _ := os.ReadFile(path)
			if strings.Contains(string(disk), `"model":`) || strings.Contains(string(disk), `"mini_model":`) {
				t.Fatal("legacy tier persisted")
			}
		})
	}
}
