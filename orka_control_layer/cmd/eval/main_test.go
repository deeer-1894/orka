package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestOfflineEvalSessionScopedFileContract(t *testing.T) {
	var mu sync.Mutex
	var conv string
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing fixture authentication")
		}
		var body map[string]any
		if r.Method == "POST" {
			json.NewDecoder(r.Body).Decode(&body)
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/controller/")
		counts[path]++
		if (path == "chat/run" || strings.HasPrefix(path, "file/")) && conv == "" {
			t.Error("execution/file operation before conversation creation")
		}
		if strings.HasPrefix(path, "file/") {
			scope, _ := body["conversation_id"].(string)
			if r.Method == "GET" {
				scope = r.URL.Query().Get("conv")
			}
			if scope == "" {
				t.Errorf("%s omitted conversation scope", path)
			}
			if conv == "" {
				conv = scope
			} else if scope != conv {
				t.Errorf("%s crossed conversations: %q != %q", path, scope, conv)
			}
		}
		switch path {
		case "conversation/create-conversation":
			conv = "created-eval-conversation"
			w.Write([]byte(`{"code":0,"data":{"conversation_id":"created-eval-conversation"}}`))
		case "file/delete":
			w.Write([]byte(`{"code":0,"data":{}}`))
		case "file/list":
			w.Write([]byte(`{"code":0,"data":[{"name":"report.txt"}]}`))
		case "file/download":
			w.Write([]byte("verified fixture"))
		case "chat/run":
			id, _ := body["conversation_id"].(string)
			if conv == "" {
				conv = id
			}
			if conv != id {
				t.Error("followup changed conversation")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: {\"type\":\"chat\",\"role\":\"assistant\",\"content\":\"fixture ready\"}\n\ndata: {\"type\":\"task\",\"action\":\"done\"}\n\n"))
		case "run/list":
			w.Write([]byte(`{"code":0,"data":{"runs":[{"status":"done","tokens":12}]}}`))
		default:
			t.Errorf("unexpected endpoint %s", path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	sc, err := run(server.URL, "", "", "fixture-token", "testdata/offline.yaml", "", "")
	if err != nil || sc.Passed != 1 {
		t.Fatalf("scorecard=%+v error=%v", sc, err)
	}
	if counts["conversation/create-conversation"] != 1 || counts["file/download"] != 2 || counts["file/list"] != 2 || counts["file/delete"] == 0 || counts["chat/run"] != 2 {
		t.Fatalf("fixture paths not covered: %v", counts)
	}
}
func TestOfflineEvalRejectsPartialAsDone(t *testing.T) {
	got := check(&client{}, "conv", expectation{}, &turnResult{status: "partial"})
	if len(got) == 0 {
		t.Fatal("partial counted as done")
	}
}

func TestEvalStopsWhenConversationCreationFails(t *testing.T) {
	for _, response := range []struct {
		name   string
		status int
		body   string
	}{
		{"storage failure", 500, `{"code":500}`},
		{"missing id", 200, `{"code":0,"data":{}}`},
		{"application error", 200, `{"code":1,"data":{"conversation_id":"invalid"}}`},
	} {
		t.Run(response.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/v1/controller/conversation/create-conversation" {
					t.Errorf("unexpected operation after failed creation: %s", r.URL.Path)
				}
				w.WriteHeader(response.status)
				w.Write([]byte(response.body))
			}))
			defer server.Close()
			c := &client{base: server.URL, token: "fixture"}
			got := c.runTask(task{ID: "creation_failure", Expect: expectation{Files: []string{"report.txt"}}})
			if got.Pass || len(got.Reasons) == 0 || calls != 1 {
				t.Fatalf("result=%+v calls=%d", got, calls)
			}
		})
	}
}
