package modelsettings

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type discoveryTransport func(*http.Request) (*http.Response, error)

func (f discoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func mockDiscovery(t *testing.T, f discoveryTransport) {
	t.Helper()
	prior := http.DefaultTransport
	http.DefaultTransport = f
	t.Cleanup(func() { http.DefaultTransport = prior })
}
func discoveryResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestDiscoverArkPresetOnlyForOfficialPlanEndpoints(t *testing.T) {
	for _, base := range []string{"https://ark.cn-beijing.volces.com/api/plan/v3", "https://ark.cn-beijing.volces.com/api/coding/v3"} {
		for _, status := range []int{404, 405} {
			t.Run(base+http.StatusText(status), func(t *testing.T) {
				calls := 0
				mockDiscovery(t, func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Method != http.MethodGet || r.URL.String() != base+"/models" || r.Header.Get("Authorization") != "Bearer test-key" {
						t.Fatalf("unexpected discovery request: %s %s", r.Method, r.URL)
					}
					return discoveryResponse(r, status, "private-provider-body"), nil
				})
				s := New(filepath.Join(t.TempDir(), "storage"))
				models, err := s.Discover(context.Background(), "alice", base, "test-key")
				if err != nil || !reflect.DeepEqual(models, []string{"doubao-seed-2.1-turbo", "doubao-seed-evolving", "doubao-seed-2.0-lite", "minimax-m3", "glm-5.3", "glm-latest", "glm-5.3-flash", "deepseek-v4-flash", "deepseek-v4-pro", "kimi-k2.7-code", "kimi-k3", "ark-code-latest"}) {
					t.Fatalf("404/405 must return preset candidates: models=%v err=%v", models, err)
				}
				if calls != 1 {
					t.Fatalf("requests=%d; fallback must not call another URL", calls)
				}
				_, exists, err := s.Get("alice")
				if err != nil || exists {
					t.Fatal("discovery persisted settings", err)
				}
			})
		}
	}
}

func TestDiscoverArkDoesNotFallbackForOtherURLsOrStatuses(t *testing.T) {
	bases := []string{
		"http://ark.cn-beijing.volces.com/api/plan/v3",
		"https://ark.cn-beijing.volces.com.attacker.test/api/plan/v3",
		"https://proxy.test/api/plan/v3",
		"https://ark.cn-beijing.volces.com:8443/api/plan/v3",
		"https://ark.cn-beijing.volces.com:443/api/plan/v3",
		"https://ark.cn-beijing.volces.com./api/plan/v3",
		"https://ark.cn-beijing.volces.com/api/v3",
		"https://ark.cn-beijing.volces.com/api/plan/v3/extra",
		"https://ark.cn-beijing.volces.com/api/plan/v30",
		"https://ark.cn-beijing.volces.com/api/plan/v3/../v3",
		"https://ark.cn-beijing.volces.com/api/%70lan/v3",
		"https://ark.cn-beijing.volces.com/api/coding/v3/extra",
	}
	for _, base := range bases {
		for _, status := range []int{404, 405} {
			t.Run(base+http.StatusText(status), func(t *testing.T) {
				calls := 0
				mockDiscovery(t, func(r *http.Request) (*http.Response, error) {
					calls++
					return discoveryResponse(r, status, "private-provider-body"), nil
				})
				models, err := New(t.TempDir()).Discover(context.Background(), "alice", base, "test-key")
				if err == nil || len(models) != 0 || strings.Contains(err.Error(), "private-provider-body") || calls != 1 {
					t.Fatalf("unexpected fallback: models=%v err=%v calls=%d", models, err, calls)
				}
			})
		}
	}
	for _, status := range []int{200, 204, 301, 302, 307, 400, 401, 403, 408, 429, 500, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			mockDiscovery(t, func(r *http.Request) (*http.Response, error) {
				calls++
				resp := discoveryResponse(r, status, "private-provider-body")
				resp.Header.Set("Location", "https://elsewhere.test/models")
				return resp, nil
			})
			models, err := New(t.TempDir()).Discover(context.Background(), "alice", "https://ark.cn-beijing.volces.com/api/plan/v3", "test-key")
			if err == nil || len(models) != 0 || strings.Contains(err.Error(), "private-provider-body") || calls != 1 {
				t.Fatalf("unexpected fallback/redirect: models=%v err=%v calls=%d", models, err, calls)
			}
		})
	}
	t.Run("network failure", func(t *testing.T) {
		mockDiscovery(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("private-network-detail") })
		models, err := New(t.TempDir()).Discover(context.Background(), "alice", "https://ark.cn-beijing.volces.com/api/plan/v3", "test-key")
		if err == nil || len(models) != 0 || strings.Contains(err.Error(), "private-network-detail") {
			t.Fatal(models, err)
		}
	})
}

func TestDiscoverMetadataPresetsKeepCredentialsScopedAndDoNotAliasURL(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "storage"))
	plan := "https://ark.cn-beijing.volces.com/api/plan/v3"
	coding := "https://ark.cn-beijing.volces.com/api/coding/v3"
	if _, err := s.Save("alice", Config{BaseURL: plan, Models: []string{"saved-model"}, Enabled: true}, "saved-test-key"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ owner, base, wantAuth, wantURL string }{
		{"alice", plan, "Bearer saved-test-key", plan + "/models"},
		{"alice", "https://ARK.CN-BEIJING.VOLCES.COM/api/plan/v3/", "Bearer saved-test-key", plan + "/models"},
		{"bob", plan, "", plan + "/models"},
		{"alice", coding, "", coding + "/models"},
	} {
		t.Run(tc.owner+tc.base, func(t *testing.T) {
			calls := 0
			mockDiscovery(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != tc.wantAuth || r.URL.String() != tc.wantURL {
					t.Fatal("saved credential scope or endpoint changed")
				}
				return discoveryResponse(r, 404, "private-provider-body"), nil
			})
			result, err := s.DiscoverWithMetadata(context.Background(), tc.owner, tc.base, "")
			if err != nil || result.Source != "preset" || len(result.Models) != 12 || !strings.Contains(result.Notice, "列表不代表密钥或套餐权限已验证") || calls != 1 {
				t.Fatal(result, err, calls)
			}
		})
	}
	saved, _, err := s.Get("alice")
	if err != nil || saved.BaseURL != plan || saved.APIKey != "saved-test-key" || !reflect.DeepEqual(saved.Models, []string{"saved-model"}) {
		t.Fatal("discovery changed saved configuration")
	}
}

func TestDiscoverCancellationCannotBecomePreset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mockDiscovery(t, func(r *http.Request) (*http.Response, error) { cancel(); return discoveryResponse(r, 404, ""), nil })
	result, err := New(t.TempDir()).DiscoverWithMetadata(ctx, "alice", "https://ark.cn-beijing.volces.com/api/plan/v3", "fake-test-key")
	if err == nil || result.Source != "" || len(result.Models) != 0 {
		t.Fatal("cancelled discovery became preset", result, err)
	}
}

func TestDiscoverNamedProfileDoesNotBorrowActiveCredential(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "storage"))
	p := Profiles{Profiles: []Config{{ID: "a", Name: "A", BaseURL: "https://same.test/v1", Models: []string{"a"}, Enabled: true}, {ID: "b", Name: "B", BaseURL: "https://same.test/v1", Models: []string{"b"}, Enabled: true}}, ActiveProfileID: "a"}
	if _, err := s.SaveProfiles("alice", p, map[string]string{"a": "key-a"}); err != nil {
		t.Fatal(err)
	}
	var auth string
	mockDiscovery(t, func(r *http.Request) (*http.Response, error) {
		auth = r.Header.Get("Authorization")
		return discoveryResponse(r, 200, `{"data":[{"id":"listed"}]}`), nil
	})
	result, err := s.DiscoverProfile(context.Background(), "alice", "b", "https://same.test/v1", "", "openai-compatible")
	if err != nil || result.Source != "remote" || auth != "" {
		t.Fatal("borrowed active profile key", err)
	}
	_, err = s.DiscoverProfile(context.Background(), "alice", "a", "https://same.test/v1", "", "openai-compatible")
	if err != nil || auth != "Bearer key-a" {
		t.Fatal("didn't use matching profile key", err)
	}
	_, err = s.DiscoverProfile(context.Background(), "alice", "a", "https://other.test/v1", "", "openai-compatible")
	if err != nil || auth != "" {
		t.Fatal("key crossed URL", err)
	}
	if _, err = s.DiscoverProfile(context.Background(), "alice", "a", "https://same.test/v1", "", "anthropic"); err == nil {
		t.Fatal("unsupported protocol accepted")
	}
}
