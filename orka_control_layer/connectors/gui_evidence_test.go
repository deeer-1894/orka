package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/orka-oss/orka_core/modelprofile"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func invokeGUIFrames(t *testing.T, frames []map[string]any) (string, error) {
	t.Helper()
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
		for _, frame := range frames {
			if err := writeGUIFrame(conn, request, frame); err != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage() // caller closes on terminal frame or timeout
	}))
	defer server.Close()
	tool := NewRunAgentTool("ws"+strings.TrimPrefix(server.URL, "http"), "fake-service-token")
	tool.Timeout = 200 * time.Millisecond
	tool.QueueTimeout = 200 * time.Millisecond
	return tool.Invoke(testGUIContext(), map[string]any{"instruction": "offline test"})
}

func receiptFrame(value string) map[string]any {
	return map[string]any{"type": "action", "action": "type", "target": "#amount",
		"evidence": map[string]any{"seq": 1, "step": 1, "kind": "action", "action": "type", "target": "#amount", "text": value, "result": "operator returned"}}
}

func TestGUIEvidenceSurvivesTerminalOutcomes(t *testing.T) {
	for _, terminal := range []string{"done", "partial", "timeout", "error", "call_user"} {
		t.Run(terminal, func(t *testing.T) {
			frames := []map[string]any{receiptFrame("0"), receiptFrame("15"),
				{"type": "observe", "evidence": map[string]any{"kind": "observation", "step": 2, "content": "Observed amount 15"}}}
			switch terminal {
			case "done", "partial":
				frames = append(frames, map[string]any{"type": "done", "outcome": terminal, "summary": "model claim"})
			case "error":
				frames = append(frames, map[string]any{"type": "error", "error": "stopped"})
			case "call_user":
				frames = append(frames, map[string]any{"type": "call_user", "reason": "help"})
			}
			got, err := invokeGUIFrames(t, frames)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{`"text":"0"`, `"text":"15"`, "Observed amount 15", "not acceptance"} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q: %s", want, got)
				}
			}
			if terminal != "done" && strings.Contains(got, "GUI task completed") {
				t.Errorf("overclaimed completion: %s", got)
			}
		})
	}
}

func TestGUIEvidenceBoundedAndWhitelisted(t *testing.T) {
	var frames []map[string]any
	for i := 0; i < 100; i++ {
		frame := receiptFrame(fmt.Sprintf("value-%03d", i))
		frame["evidence"].(map[string]any)["_handle"] = "private-handle"
		frame["evidence"].(map[string]any)["_thought"] = "private-thought"
		frames = append(frames, frame)
	}
	frames = append(frames, map[string]any{"type": "done", "summary": strings.Repeat("x", 100000)})
	got, err := invokeGUIFrames(t, frames)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "value-099") || strings.Contains(got, "value-000") {
		t.Errorf("wrong retained window: %.300s", got)
	}
	if strings.Contains(got, "private-") || len(got) > 50000 {
		t.Errorf("unbounded or private evidence: len=%d", len(got))
	}
}

func TestGUIUnknownTimeoutDoesNotAssertUnchangedPage(t *testing.T) {
	got, err := invokeGUIFrames(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "Nothing was changed") {
		t.Fatal(got)
	}
}

func TestGUIReceiptLegacyAndSensitiveCompatibility(t *testing.T) {
	private := receiptFrame("secret")
	fields := private["evidence"].(map[string]any)
	fields["input_redacted"] = true
	fields["target"], fields["result"] = "secret", "secret"
	got, err := invokeGUIFrames(t, []map[string]any{
		{"type": "action", "action": "click", "target": "Old button"}, private,
		{"type": "done", "summary": "legacy done"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "secret") {
		t.Fatal("sensitive evidence leaked")
	}
	for _, want := range []string{"Old button", `"legacy":true`, `"input_redacted":true`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func TestGUIReceiptWindowHasAbsoluteSequenceAndOmissionCount(t *testing.T) {
	var evidence guiEvidence
	for i := 0; i < 100; i++ {
		evidence.add(receiptFrame(fmt.Sprint(i)))
	}
	var result struct {
		Evidence []struct {
			Seq  int
			Text string
		}
		Omitted int `json:"omitted_events"`
		Actions int `json:"recorded_actions"`
	}
	if err := json.Unmarshal([]byte(evidence.result("done", "claim")), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Evidence) != 24 || result.Omitted != 76 || result.Actions != 100 {
		t.Fatalf("bad bounds: %+v", result)
	}
	if result.Evidence[0].Seq != 77 || result.Evidence[23].Seq != 100 || result.Evidence[23].Text != "99" {
		t.Fatalf("bad sequence: %+v", result.Evidence)
	}
}

func testGUIContext() context.Context {
	ctx := WithGUIIdentity(context.Background(), GUIIdentity{"fake-owner", "fake-conversation", "fake-run"})
	return modelprofile.WithContext(ctx, modelprofile.Snapshot{Protocol: modelprofile.OpenAICompatible,
		BaseURL: "http://fake-provider.test/v1", APIKey: "fake-model-key", Model: "selected-model",
		Capabilities: modelprofile.Capabilities{Vision: true}})
}

// Echo the execution scope like the authenticated Python executor.
func writeGUIFrame(conn *websocket.Conn, request, frame map[string]any) error {
	frame["session_id"] = request["session_id"]
	frame["run_id"] = request["identity"].(map[string]any)["run_id"]
	return conn.WriteJSON(frame)
}
