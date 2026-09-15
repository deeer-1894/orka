package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/modelsettings"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUserModelRuntimeSnapshot(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		calls = append(calls, r.URL.Path+"|"+req.Model+"|"+r.Header.Get("Authorization"))
		mu.Unlock()
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"from-user-provider\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
		} else {
			w.Write([]byte(`{"choices":[{"message":{"content":"from-user-provider"},"finish_reason":"stop"}]}`))
		}
	}))
	defer ts.Close()
	svc, _ := testService(t, llm.NewMock(llm.Response{Content: "deployment", FinishReason: "stop"}))
	svc.Cfg.Storage.BaseStoragePath = t.TempDir()
	svc.ModelSettings = modelsettings.New(svc.Cfg.Storage.BaseStoragePath)
	for _, owner := range []string{"alice", "bob"} {
		_, err := svc.ModelSettings.Save(owner, modelsettings.Config{BaseURL: ts.URL + "/" + owner, Model: owner + "-main", MiniModel: owner + "-fast", Models: []string{"manual"}, Enabled: true}, owner+"-secret")
		if err != nil {
			t.Fatal(err)
		}
	}
	// Save during ToolsFor, after the request snapshot was taken. Main, mini,
	// routing and delegates must still use the original snapshot for this run.
	svc.ToolsFor = func(ctx context.Context, req ChatRunRequest) ([]agent.BaseTool, func(), error) {
		if req.UserEmail == "alice" {
			_, err := svc.ModelSettings.Save("alice", modelsettings.Config{BaseURL: ts.URL + "/changed", Model: "changed", Enabled: true}, "changed-secret")
			if err != nil {
				t.Error(err)
			}
			snapshot := svc.modelsForContext(ctx)
			client, name := snapshot.modelFor("mini")
			if name != "alice-main" {
				t.Error(name)
			}
			if _, err := client.Chat(ctx, llm.Request{Model: name}); err != nil {
				t.Error(err)
			}
		}
		return nil, nil, nil
	}
	var wg sync.WaitGroup
	for _, owner := range []string{"alice", "bob"} {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			col := &collector{}
			svc.Run(context.Background(), ChatRunRequest{UserEmail: owner, ConversationID: owner, Message: "hello"}, col.sink)
			chats := col.byType("chat")
			if len(chats) == 0 || chats[len(chats)-1].Content != "from-user-provider" || len(chats[len(chats)-1].Meta.ModelProfile) != 64 {
				t.Error("no chat", owner)
			}
		}(owner)
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(calls, "\n")
	for _, expected := range []string{"/alice/chat/completions|alice-main|Bearer alice-secret", "/alice/chat/completions|alice-main|Bearer alice-secret", "/bob/chat/completions|bob-main|Bearer bob-secret"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("missing %s in %s", expected, joined)
		}
	}
	if strings.Contains(joined, "changed") || svc.Cfg.LLM.Model != "m" {
		t.Fatal("snapshot/global mutation", joined)
	}
}

// Serve real OpenAI-format streaming responses through the production client.
func modelFixture(t *testing.T, respond func(int, string) llm.Response) (*httptest.Server, *[]string, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	calls := []string{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if r.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("wrong credential")
		}
		mu.Lock()
		n := len(calls)
		calls = append(calls, req.Model)
		mu.Unlock()
		resp := respond(n, req.Model)
		delta := map[string]any{"content": resp.Content}
		if len(resp.ToolCalls) > 0 {
			calls := []map[string]any{}
			for i, tc := range resp.ToolCalls {
				calls = append(calls, map[string]any{"index": i, "id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": tc.Arguments}})
			}
			delta["tool_calls"] = calls
		}
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": resp.FinishReason}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
	t.Cleanup(ts.Close)
	return ts, &calls, &mu
}
func configuredModelService(t *testing.T, base string) *ChatService {
	t.Helper()
	svc, _ := testService(t, llm.NewMock(llm.Response{Content: "deployment", FinishReason: "stop"}))
	svc.Cfg.Storage.BaseStoragePath = t.TempDir()
	svc.ModelSettings = modelsettings.New(svc.Cfg.Storage.BaseStoragePath)
	_, err := svc.ModelSettings.Save("owner", modelsettings.Config{BaseURL: base, Model: "user-main", MiniModel: "user-mini", Models: []string{"manual"}, Enabled: true}, "private-key")
	if err != nil {
		t.Fatal(err)
	}
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) { return nil, nil, nil }
	return svc
}
func TestUserModelSelectionPaths(t *testing.T) {
	for _, tc := range []struct {
		name, version, prompt, want string
		fast, resume                bool
		invalid                     bool
	}{
		{name: "main", want: "user-main"}, {name: "legacy-mini-selection", version: "mini", invalid: true},
		{name: "manual", version: "manual", want: "manual"}, {name: "unknown", version: "unknown", invalid: true},
		{name: "auto-default", version: "auto", prompt: "hello", want: "user-main"},
		{name: "auto-complex", version: "auto", prompt: "compare two approaches", want: "user-main"},
		{name: "fast-path", want: "user-main", fast: true},
		{name: "recovery", want: "user-main", resume: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, calls, mu := modelFixture(t, func(int, string) llm.Response { return llm.Response{Content: "served", FinishReason: "stop"} })
			svc := configuredModelService(t, ts.URL)
			svc.DisableFastPath = !tc.fast
			req := ChatRunRequest{UserEmail: "owner", ConversationID: "conv", Message: tc.prompt, SelectedVersion: tc.version}
			if req.Message == "" {
				req.Message = "hello"
			}
			if tc.resume {
				req.resumeFrom = &runResume{Messages: []*schema.Message{schema.UserMessage("earlier work")}, Reason: "failed"}
			}
			col := &collector{}
			status := svc.Run(context.Background(), req, col.sink)
			if tc.invalid {
				if status != db.RunFailed || len(col.byType("task")) == 0 {
					t.Fatalf("invalid selection status=%s", status)
				}
				mu.Lock()
				defer mu.Unlock()
				if len(*calls) != 0 {
					t.Fatal("invalid selection called provider", *calls)
				}
				return
			}
			if status != db.RunDone {
				t.Fatalf("status=%s events=%+v", status, col.msgs)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(*calls) != 1 || (*calls)[0] != tc.want {
				t.Fatalf("calls=%v want=%s", *calls, tc.want)
			}
		})
	}
}

func TestUserModelDelegateRetainsSnapshot(t *testing.T) {
	for _, selection := range []string{ModelAuto, "manual"} {
		t.Run(selection, func(t *testing.T) {
			var svc *ChatService
			ts, calls, mu := modelFixture(t, func(n int, model string) llm.Response {
				if n == 0 {
					if _, err := svc.ModelSettings.Save("owner", modelsettings.Config{BaseURL: "http://localhost:1/v1", Model: "changed", Enabled: true}, "new-key"); err != nil {
						t.Error(err)
					}
					return gateCall("delegate", "task", `{"subagent_type":"researcher","description":"verify facts"}`)
				}
				return llm.Response{Content: "served", FinishReason: "stop"}
			})
			svc = configuredModelService(t, ts.URL)
			svc.Cfg.Agent.MultiAgent = true
			svc.Cfg.Agent.SubAgents = []config.SubAgentConfig{{Name: "researcher", Description: "verify facts", Tools: []string{"file_read"}}}
			svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
				return deepTestTools(), nil, nil
			}
			col := &collector{}
			status := svc.Run(context.Background(), ChatRunRequest{UserEmail: "owner", ConversationID: "conv", Message: "delegate verification", SelectedVersion: selection}, col.sink)
			if status != db.RunDone {
				t.Fatalf("status %s: %+v", status, col.msgs)
			}
			mu.Lock()
			defer mu.Unlock()
			want := "user-main"
			if selection == "manual" {
				want = selection
			}
			if strings.Join(*calls, ",") != strings.Join([]string{want, want, want}, ",") {
				t.Fatal(*calls)
			}
		})
	}
}

func TestUserModelRedirectAndErrorRedaction(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	svc := configuredModelService(t, source.URL)
	snapshot, err := svc.resolveModels("owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = snapshot.main.Chat(context.Background(), llm.Request{Model: "user-main"}); err == nil || reached.Load() {
		t.Fatal("redirect followed", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "upstream echoes private-key", 401) }))
	defer bad.Close()
	_, err = svc.ModelSettings.Save("owner", modelsettings.Config{BaseURL: bad.URL, Model: "m", Enabled: true}, "private-key")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = svc.resolveModels("owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = snapshot.main.Chat(context.Background(), llm.Request{Model: "m"}); err == nil || strings.Contains(err.Error(), "private-key") {
		t.Fatal("key leak", err)
	}
}

func TestUserModelKillUsesSharedRegistry(t *testing.T) {
	started := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer ts.Close()
	svc := configuredModelService(t, ts.URL)
	col := &collector{}
	done := make(chan struct{})
	go func() {
		svc.Run(context.Background(), ChatRunRequest{UserEmail: "owner", ConversationID: "kill", Message: "wait"}, col.sink)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("no model request")
	}
	if !svc.Kill("kill") {
		t.Fatal("run not in shared registry")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("kill did not stop user client")
	}
}

func TestUserModelUnreadableSettingsFailClosed(t *testing.T) {
	svc := configuredModelService(t, "http://localhost:1/v1")
	files, err := filepath.Glob(filepath.Join(filepath.Dir(svc.Cfg.Storage.BaseStoragePath), "model-settings", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	if err = os.WriteFile(files[0], []byte(`broken private-key`), 0600); err != nil {
		t.Fatal(err)
	}
	col := &collector{}
	if status := svc.Run(context.Background(), ChatRunRequest{UserEmail: "owner", ConversationID: "corrupt", Message: "hello"}, col.sink); status != db.RunFailed {
		t.Fatal(status)
	}
	tasks := col.byType("task")
	if len(tasks) == 0 || tasks[len(tasks)-1].Action != "failed" {
		t.Fatal("no terminal failure")
	}
	if b, _ := json.Marshal(col.msgs); strings.Contains(string(b), "private-key") {
		t.Fatal("key in failure event")
	}
	if _, err = svc.ModelConfigForUser("owner"); err == nil {
		t.Fatal("listing silently fell back")
	}
}

func TestUserModelFollowupsUsesExplicitChoice(t *testing.T) {
	var mu sync.Mutex
	var called []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		called = append(called, req.Model)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("wrong owner credential")
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"[\"Next question?\"]"},"finish_reason":"stop"}]}`))
	}))
	defer ts.Close()
	svc := configuredModelService(t, ts.URL)
	snapshot, err := svc.resolveModels("owner")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.SuggestFollowupsForUser(context.Background(), "owner", "manual", snapshot.profile, "question", "answer")
	if err != nil || len(got) != 1 || got[0] != "Next question?" {
		t.Fatal(got, err)
	}
	if _, err = svc.SuggestFollowupsForUser(context.Background(), "owner", "removed", snapshot.profile, "question", "answer"); err != ErrModelSelection {
		t.Fatal("invalid selection accepted", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(called, ",") != "manual" {
		t.Fatal(called)
	}
}

func TestUserModelLegacyBuilderInputsIgnored(t *testing.T) {
	for _, role := range []string{"main", "mini"} {
		t.Run(role, func(t *testing.T) {
			selected := llm.NewMock(llm.Response{Content: "selected", FinishReason: "stop"})
			obsolete := llm.NewMock(llm.Response{Content: "obsolete", FinishReason: "stop"})
			specs := []config.SubAgentConfig{{Name: "worker", Description: "work", Tools: []string{"file_read"}, Model: role}}
			subs, err := BuildEinoSubAgents(context.Background(), selected, "chosen", obsolete, "old-mini", deepTestTools(), specs)
			if err != nil {
				t.Fatal(err)
			}
			result, err := RunEinoOnce(context.Background(), subs[0], "do work")
			if err != nil || result != "selected" || selected.Calls() != 1 || selected.Requests[0].Model != "chosen" || obsolete.Calls() != 0 {
				t.Fatalf("result=%s err=%v selected=%d obsolete=%d", result, err, selected.Calls(), obsolete.Calls())
			}
		})
	}
}

func TestUserModelConcurrentRunsShareLimit(t *testing.T) {
	t.Setenv("ORKA_LLM_MAX_CONCURRENCY", "1")
	t.Setenv("ORKA_LLM_MIN_INTERVAL_MS", "0")
	var active, maximum, total atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		total.Add(1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		time.Sleep(40 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer ts.Close()
	svc := configuredModelService(t, ts.URL)
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		ready <- struct{}{}
		<-release
		return nil, nil, nil
	}
	var wg sync.WaitGroup
	for _, conv := range []string{"one", "two"} {
		wg.Add(1)
		go func(conv string) {
			defer wg.Done()
			if status := svc.Run(context.Background(), ChatRunRequest{UserEmail: "owner", ConversationID: conv, Message: "hello"}, (&collector{}).sink); status != db.RunDone {
				t.Error(status)
			}
		}(conv)
	}
	<-ready
	<-ready
	close(release)
	wg.Wait()
	if total.Load() != 2 || maximum.Load() != 1 {
		t.Fatalf("calls=%d peak=%d", total.Load(), maximum.Load())
	}
}

func TestUserModelFollowupsSkipChangedProfile(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"choices":[{"message":{"content":"[\"Next?\"]"},"finish_reason":"stop"}]}`))
	}))
	defer ts.Close()
	svc := configuredModelService(t, ts.URL)
	snapshot, err := svc.resolveModels("owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.profile) != 64 || strings.Contains(snapshot.profile, "private-key") {
		t.Fatal("unsafe profile")
	}
	if _, err = svc.SuggestFollowupsForUser(context.Background(), "owner", "manual", "", "Q", "A"); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ModelSettings.Save("owner", modelsettings.Config{BaseURL: ts.URL + "/changed", Models: []string{"manual"}, Enabled: true}, "different-key")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := svc.SuggestFollowupsForUser(context.Background(), "owner", "manual", snapshot.profile, "Q", "A"); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	current, err := svc.resolveModels("owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ModelSettings.Save("owner", modelsettings.Config{BaseURL: ts.URL + "/changed", Models: []string{"manual"}, Enabled: true}, "rotated-key"); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.SuggestFollowupsForUser(context.Background(), "owner", "manual", current.profile, "Q", "A"); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	if calls.Load() != 0 {
		t.Fatal("old answer was sent to changed provider")
	}
}

// Exercise the public recovery entry point, where the selected model used to
// disappear before Run resolved the owner's current Auto default.
func TestResumeRunRetainsRecordedModel(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("manual model differs from Auto", func(mt *mtest.T) {
		ts, calls, mu := modelFixture(mt.T, func(int, string) llm.Response { return llm.Response{Content: "resumed", FinishReason: "stop"} })
		svc := configuredModelService(mt.T, ts.URL)
		svc.Msg.Store = &db.Storage{Runs: mt.Coll}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{
				{Key: "run_id", Value: "recorded"}, {Key: "owner_email", Value: "owner"},
				{Key: "conversation_id", Value: "conv"}, {Key: "prompt", Value: "continue work"},
				{Key: "model", Value: "manual"}, {Key: "resumable", Value: true}, {Key: "status", Value: db.RunFailed},
			}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nModified", Value: 1}),
		)
		j := newRunJournal(svc.Cfg.Storage.BaseStoragePath, "recorded", []*schema.Message{schema.UserMessage("continue work")})
		j.append(schema.AssistantMessage("earlier progress", nil))
		if !j.flush() {
			mt.Fatal("journal persistence failed")
		}
		svc.ToolsFor = func(_ context.Context, req ChatRunRequest) ([]agent.BaseTool, func(), error) {
			// Recovery storage was exercised above; the new execution uses the usual
			// in-memory service fixture so unrelated persistence is outside this test.
			svc.Msg.Store = nil
			return nil, nil, nil
		}
		status, err := svc.ResumeRun(context.Background(), "recorded", "owner", (&collector{}).sink)
		if err != nil || status != db.RunDone {
			mt.Fatalf("status=%s err=%v", status, err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(*calls) != 1 || (*calls)[0] != "manual" {
			mt.Fatalf("recovery called %v, want recorded manual model", *calls)
		}
	})
}
