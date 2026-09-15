package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/modelprofile"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGUIRequiresTrustedRunIdentityBeforeConnecting(t *testing.T) {
	tool := NewRunAgentTool("ws://127.0.0.1:1", "fake-token")
	_, err := tool.Invoke(context.Background(), map[string]any{"instruction": "read", "owner_id": "spoof", "conversation_id": "spoof", "run_id": "spoof"})
	if err == nil || !strings.Contains(err.Error(), "trusted") {
		t.Fatalf("accepted untrusted identity: %v", err)
	}
}

type guiTestUsageSink struct{ prompt, completion int }

func (s *guiTestUsageSink) AddUsage(prompt, completion int) {
	s.prompt += prompt
	s.completion += completion
}

func TestGUITransportsSelectedSnapshotAndBooksUsageOnce(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-service-token" {
			t.Error("missing service authentication")
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		if conn.ReadJSON(&request) != nil {
			return
		}
		received <- request
		receipt := map[string]any{"type": "usage", "call_id": "one", "model": "selected-model", "known": true, "prompt_tokens": 31, "completion_tokens": 7, "total_tokens": 38}
		for _, frame := range []map[string]any{{"type": "started"}, receipt, receipt,
			{"type": "usage", "call_id": "two", "model": "selected-model", "total_tokens": 17, "known": false},
			{"type": "done", "summary": "done", "usage": map[string]any{"calls": 2, "total_tokens": 55}}} {
			writeGUIFrame(conn, request, frame)
		}
	}))
	defer server.Close()
	sink := &guiTestUsageSink{}
	ctx := llm.WithUsageSink(testGUIContext(), sink)
	ctx = agent.WithMeta(ctx, messages.Meta{UserEmail: "meta-owner", ConversationID: "meta-conversation", RunID: "run-selected"})
	tool := NewRunAgentTool("ws"+strings.TrimPrefix(server.URL, "http"), "fake-service-token")
	got, err := tool.Invoke(ctx, map[string]any{"instruction": "read", "identity": map[string]any{"owner_id": "spoof"}, "model": "spoof"})
	if err != nil {
		t.Fatal(err)
	}
	request := <-received
	identity := request["identity"].(map[string]any)
	config := request["model_config"].(map[string]any)
	if identity["owner_id"] != "meta-owner" || identity["run_id"] != "run-selected" {
		t.Fatalf("wrong identity: %v", identity)
	}
	if config["model"] != "selected-model" || config["api_key"] != "fake-model-key" || config["vision_verified"] != true {
		t.Fatal("selected model snapshot not transported")
	}
	if sink.prompt != 31 || sink.completion != 24 {
		t.Fatalf("usage double-counted or lost: %+v", sink)
	}
	if strings.Contains(got, "fake-model-key") || strings.Contains(got, "fake-service-token") {
		t.Fatal("credentials leaked")
	}
	for _, want := range []string{`"calls":2`, `"total_tokens":55`, `"unknown_calls":1`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s: %s", want, got)
		}
	}
}

func TestGUIQueueAndExecutionHaveSeparateTimeouts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		if err := conn.ReadJSON(&request); err != nil {
			return
		}
		writeGUIFrame(conn, request, map[string]any{"type": "queued"})
		time.Sleep(150 * time.Millisecond) // longer than execution allowance, within queue allowance
		writeGUIFrame(conn, request, map[string]any{"type": "started"})
		writeGUIFrame(conn, request, map[string]any{"type": "done", "summary": "ran after waiting"})
	}))
	defer server.Close()
	tool := NewRunAgentTool("ws"+strings.TrimPrefix(server.URL, "http"), "fake-token")
	tool.Timeout, tool.QueueTimeout = 50*time.Millisecond, time.Second
	got, err := tool.Invoke(testGUIContext(), map[string]any{"instruction": "read"})
	if err != nil || !strings.Contains(got, `"status":"done"`) {
		t.Fatalf("queue consumed execution allowance: %s %v", got, err)
	}
}

func TestGUIMissingSnapshotAndPublicTransportFailClosed(t *testing.T) {
	identity := WithGUIIdentity(context.Background(), GUIIdentity{"a", "c", "r"})
	tool := NewRunAgentTool("ws://127.0.0.1:1", "fake-token")
	if _, err := tool.Invoke(identity, map[string]any{"instruction": "read"}); err == nil {
		t.Fatal("missing config accepted")
	}
	badProtocol := modelprofile.WithContext(identity, modelprofile.Snapshot{Protocol: "unsupported"})
	if _, err := tool.Invoke(badProtocol, map[string]any{"instruction": "read"}); err == nil {
		t.Fatal("unsupported protocol accepted")
	}
	if _, err := dialGUI(context.Background(), "ws://203.0.113.1/socket", "fake-token"); err == nil {
		t.Fatal("public endpoint accepted")
	}
	if _, err := dialGUI(context.Background(), "ws://127.0.0.1:1", ""); err == nil {
		t.Fatal("unauthenticated endpoint accepted")
	}
	cfg := GUIModelConfig{BaseURL: "http://fake.test/v1", APIKey: "fake-secret", Model: "chosen", VisionVerified: true}
	data, _ := json.Marshal(cfg)
	if strings.Contains(string(data)+fmt.Sprintf("%+v %#v", cfg, cfg), "fake-secret") {
		t.Fatal("config logs leak key")
	}
}

type guiTestAccountant struct {
	reserveErr, settleErr error
	spec                  llm.ExternalCallSpec
	response              llm.Response
	settled               int
}

func (a *guiTestAccountant) Begin(ctx context.Context, _ llm.Request) (func(context.Context, llm.Response, error) error, error) {
	a.spec, _ = llm.ExternalCallFrom(ctx)
	if a.reserveErr != nil {
		return nil, a.reserveErr
	}
	return func(ctx context.Context, response llm.Response, _ error) error {
		a.response = response
		a.settled++
		if ctx.Err() != nil {
			return fmt.Errorf("settlement inherited cancellation")
		}
		return a.settleErr
	}, nil
}
func TestGUIReservesBeforeDispatchAndSettlesActualUsage(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(fmt.Sprint(denied), func(t *testing.T) {
			received := make(chan bool, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				var request map[string]any
				if conn.ReadJSON(&request) != nil {
					received <- false
					return
				}
				received <- true
				writeGUIFrame(conn, request, map[string]any{"type": "usage", "call_id": "actual", "prompt_tokens": 17, "completion_tokens": 5, "total_tokens": 22})
				writeGUIFrame(conn, request, map[string]any{"type": "done", "summary": "done", "usage": map[string]any{"calls": 1}})
			}))
			defer server.Close()
			accountant := &guiTestAccountant{}
			if denied {
				accountant.reserveErr = fmt.Errorf("quota exhausted")
			}
			sink := &guiTestUsageSink{}
			ctx := llm.WithCallAccountant(llm.WithUsageSink(testGUIContext(), sink), accountant)
			tool := NewRunAgentTool("ws"+strings.TrimPrefix(server.URL, "http"), "fake-token")
			_, err := tool.Invoke(ctx, map[string]any{"instruction": "read"})
			if (<-received) == denied {
				t.Fatal("request dispatch ignored budget admission")
			}
			if denied {
				if err == nil {
					t.Fatal("budget denial ignored")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if accountant.spec.Source != "gui" || accountant.spec.PromptTokens != 32768 || accountant.spec.MaxSteps != 10 || accountant.spec.MaxCompletionTokens != 4096 {
				t.Fatalf("wrong reservation: %+v", accountant.spec)
			}
			if accountant.settled != 1 || !accountant.response.Usage.Known || accountant.response.Usage.TotalTokens != 22 {
				t.Fatalf("wrong settlement: %+v", accountant)
			}
			if sink.prompt != 0 || sink.completion != 0 {
				t.Fatal("double-counted parent legacy sink")
			}
		})
	}
}
func TestGUIIncompleteUsageRemainsUnknownAndSettlementErrorsPropagate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		if err := conn.ReadJSON(&request); err != nil {
			return
		}
		writeGUIFrame(conn, request, map[string]any{"type": "done", "outcome": "partial", "summary": "provider interrupted", "usage": map[string]any{"calls": 1}})
	}))
	defer server.Close()
	accountant := &guiTestAccountant{settleErr: fmt.Errorf("ledger unavailable")}
	tool := NewRunAgentTool("ws"+strings.TrimPrefix(server.URL, "http"), "fake-token")
	_, err := tool.Invoke(llm.WithCallAccountant(testGUIContext(), accountant), map[string]any{"instruction": "read"})
	if accountant.settled != 1 || accountant.response.Usage.Known {
		t.Fatalf("unknown usage released: %+v", accountant)
	}
	if !errors.Is(err, accountant.settleErr) {
		t.Fatalf("settlement error hidden: %v", err)
	}
}

func TestGUICancelAfterDispatchSettlesUnknownWithPartialEvidence(t *testing.T) {
	buffered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		if err := conn.ReadJSON(&request); err != nil {
			return
		}
		for _, frame := range []map[string]any{
			{"type": "started"}, receiptFrame("15"),
			{"type": "progress", "task_memory": map[string]any{"goals": []any{map[string]any{"goal": "initial", "status": "complete", "observation": "Value 17", "source": "model_observation"}}}},
			{"type": "usage", "call_id": "completed-before-cancel", "prompt_tokens": 17, "completion_tokens": 5, "total_tokens": 22},
		} {
			if writeGUIFrame(conn, request, frame) != nil {
				return
			}
		}
		// Fill the reader buffer while the usage callback is paused. These
		// screenshots must never reach SSE once that callback cancels.
		for n := 0; n < 80; n++ {
			if writeGUIFrame(conn, request, map[string]any{"type": "screenshot", "data": "late-screen"}) != nil {
				break
			}
		}
		conn.WriteJSON(map[string]any{"type": "action", "action": "type", "target": "foreign-late", "session_id": request["session_id"], "run_id": "other-run"})
		close(buffered)
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, _ = conn.ReadMessage() // disconnect must stop remote execution
	}))
	defer server.Close()
	accountant := &guiTestAccountant{}
	sink := &guiTestUsageSink{}
	ctx, cancel := context.WithCancel(testGUIContext())
	defer cancel()
	ctx = agent.WithEmit(ctx, func(message messages.Message) {
		if ctx.Err() != nil {
			t.Error("SSE event emitted after cancellation")
		}
		if message.Action == "screenshot" {
			t.Error("buffered screenshot leaked")
		}
		if message.Action == "usage" {
			select {
			case <-buffered:
			case <-time.After(5 * time.Second):
				t.Error("fake peer did not fill buffer")
			}
			cancel()
		}
	})
	ctx = llm.WithCallAccountant(llm.WithUsageSink(ctx, sink), accountant)
	tool := NewRunAgentTool("ws"+strings.TrimPrefix(server.URL, "http"), "fake-token")
	got, err := tool.Invoke(ctx, map[string]any{"instruction": "read"})
	if strings.Contains(got, "foreign-late") {
		t.Error("foreign buffered evidence retained")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel lost: %v", err)
	}
	if accountant.settled != 1 || accountant.response.Usage.Known || accountant.response.Usage.TotalTokens != 22 {
		t.Fatalf("partial usage treated as complete or lost: %+v", accountant)
	}
	if sink.prompt != 0 || sink.completion != 0 {
		t.Fatal("legacy sink double charged")
	}
	for _, want := range []string{`"status":"cancelled"`, `"text":"15"`, `"total_tokens":22`, `"complete":false`, `"observation":"Value 17"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s: %s", want, got)
		}
	}
}

func TestGUIFramesRequireExecutionScopeAndTrustedSSEMeta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		if conn.ReadJSON(&request) != nil {
			return
		}
		session := request["session_id"]
		run := request["identity"].(map[string]any)["run_id"]
		for _, frame := range []map[string]any{
			{"type": "screenshot", "data": "untagged"},
			{"type": "screenshot", "data": "other-run", "session_id": session, "run_id": "another-run"},
			{"type": "action", "action": "type", "target": "other-session", "session_id": "another-session", "run_id": run},
			{"type": "usage", "call_id": "wrong-usage", "prompt_tokens": 999, "completion_tokens": 999, "total_tokens": 1998, "session_id": session, "run_id": "another-run"},
			{"type": "screenshot", "data": "own-screen", "session_id": session, "run_id": run},
			{"type": "action", "action": "click", "target": "own-action", "session_id": session, "run_id": run},
			{"type": "done", "summary": "own-done", "session_id": session, "run_id": run},
		} {
			if conn.WriteJSON(frame) != nil {
				return
			}
		}
	}))
	defer server.Close()
	var events []messages.Message
	sink := &guiTestUsageSink{}
	ctx := agent.WithMeta(testGUIContext(), messages.Meta{RunID: "run-a", UserEmail: "owner-a", ConversationID: "conversation-a", TraceID: "trace-a"})
	ctx = llm.WithUsageSink(agent.WithEmit(ctx, func(m messages.Message) { events = append(events, m) }), sink)
	tool := NewRunAgentTool("ws"+strings.TrimPrefix(server.URL, "http"), "fake-token")
	got, err := tool.Invoke(ctx, map[string]any{"instruction": "read"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Errorf("unscoped or other execution leaked: %+v", events)
	}
	for _, event := range events {
		if event.Meta.RunID != "run-a" || event.Meta.ConversationID != "conversation-a" || event.Meta.UserEmail != "owner-a" || event.Meta.TraceID != "trace-a" {
			t.Errorf("missing trusted SSE identity: %+v", event.Meta)
		}
	}
	if sink.prompt != 0 || sink.completion != 0 || strings.Contains(got, "other-session") {
		t.Fatalf("foreign evidence/usage collected: %s %+v", got, sink)
	}
}

func TestGUISelectedPolicyMatchesWireAndBudgetReservation(t *testing.T) {
	for _, tc := range []struct{ cap, want int }{{700, 700}, {8192, 4096}, {0, 4096}} {
		t.Run(fmt.Sprint(tc.cap), func(t *testing.T) {
			received := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				var req map[string]any
				if conn.ReadJSON(&req) != nil {
					return
				}
				received <- req["model_config"].(map[string]any)
				writeGUIFrame(conn, req, map[string]any{"type": "done", "summary": "done"})
			}))
			defer server.Close()
			ctx := modelprofile.WithContext(testGUIContext(), modelprofile.Snapshot{Protocol: modelprofile.OpenAICompatible, BaseURL: "http://fake.test/v1", Model: "any-model", Capabilities: modelprofile.Capabilities{Vision: true}, Policy: modelprofile.CallPolicy{FirstMaxTokens: 300, MaxTokens: tc.cap, TimeoutSeconds: 9, ReasoningEffort: "none"}})
			accountant := &guiTestAccountant{}
			tool := NewRunAgentTool("ws"+strings.TrimPrefix(server.URL, "http"), "fake-token")
			_, err := tool.Invoke(llm.WithCallAccountant(ctx, accountant), map[string]any{"instruction": "read", "policy": map[string]any{"max_tokens": 999999}})
			if err != nil {
				t.Fatal(err)
			}
			config := <-received
			policy, ok := config["policy"].(map[string]any)
			if !ok {
				t.Fatal("selected policy absent from wire")
			}
			if policy["max_tokens"] != float64(tc.want) || policy["first_max_tokens"] != float64(300) || policy["timeout_seconds"] != float64(9) || policy["reasoning_effort"] != "none" {
				t.Fatalf("wrong effective policy: %+v", policy)
			}
			if accountant.spec.MaxCompletionTokens != tc.want {
				t.Fatalf("reservation %d mismatches wire %d", accountant.spec.MaxCompletionTokens, tc.want)
			}
		})
	}
}

func TestGUIProgressSurvivesTerminalAndTimeoutWithoutBecomingReceipt(t *testing.T) {
	for _, terminal := range []string{"done", "error", "timeout"} {
		t.Run(terminal, func(t *testing.T) {
			frames := []map[string]any{{"type": "progress", "task_memory": map[string]any{"goals": []any{map[string]any{"goal": "initial", "status": "complete", "observation": "Value 17", "source": "model_observation", "observed_step": 0, "observation_seq": 1, "screenshot_sha256": strings.Repeat("a", 64), "_raw": "private"}}}}}
			if terminal != "timeout" {
				frames = append(frames, map[string]any{"type": terminal, "summary": "done", "error": "stopped"})
			}
			got, err := invokeGUIFrames(t, frames)
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err = json.Unmarshal([]byte(got), &result); err != nil {
				t.Fatal(err)
			}
			if _, ok := result["task_memory"]; !ok {
				t.Fatal("model observations lost from result")
			}
			if strings.Contains(got, "private") || !strings.Contains(got, "Value 17") || result["recorded_actions"] != float64(0) {
				t.Fatalf("invalid progress/receipts: %s", got)
			}
		})
	}
}

func TestGUIExplicitUnknownReceiptCannotBecomeCompleteUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var req map[string]any
		if conn.ReadJSON(&req) != nil {
			return
		}
		writeGUIFrame(conn, req, map[string]any{"type": "usage", "call_id": "incomplete", "known": false, "prompt_tokens": 17, "completion_tokens": 5, "total_tokens": 22})
		writeGUIFrame(conn, req, map[string]any{"type": "done", "summary": "partial counts", "usage": map[string]any{"calls": 1}})
	}))
	defer server.Close()
	accountant := &guiTestAccountant{}
	tool := NewRunAgentTool("ws"+strings.TrimPrefix(server.URL, "http"), "fake-token")
	got, err := tool.Invoke(llm.WithCallAccountant(testGUIContext(), accountant), map[string]any{"instruction": "read"})
	if err != nil {
		t.Fatal(err)
	}
	if accountant.response.Usage.Known || !strings.Contains(got, `"unknown_calls":1`) {
		t.Fatalf("explicit incomplete receipt became known: %s", got)
	}
	if accountant.response.Usage.TotalTokens != 22 {
		t.Fatal("partial known counts discarded")
	}
}
