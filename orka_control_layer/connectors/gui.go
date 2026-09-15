// Package connectors holds adapters to external executors. RunAgentTool wraps
// the remote GUI agent as a BaseTool: it opens a WebSocket to GUI_AGENT_WS_URL,
// drives one task, surfaces browser events into the run's SSE stream (via the
// emit side-channel on the context), and returns a bounded execution evidence window alongside the final summary.
package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/ws"
)

// RunAgentTool adapts the GUI executor to agent.BaseTool.
type RunAgentTool struct {
	WSURL    string
	Token    string // shared secret sent as Authorization: Bearer on the WS handshake
	MaxSteps int
	Timeout  time.Duration
}

// NewRunAgentTool builds a run_agent tool targeting wsURL. token may be empty
// (dev); when set it is sent so the GUI executor can authenticate the caller.
// MaxSteps bounds one browser invocation; the GUI agent also stops early on
// no-progress (repeated actions) and returns a grounded page snapshot, so a
// flailing run ends in a few steps rather than burning the whole budget.
func NewRunAgentTool(wsURL, token string) *RunAgentTool {
	return &RunAgentTool{WSURL: wsURL, Token: token, MaxSteps: 10, Timeout: 90 * time.Second}
}

func (*RunAgentTool) Name() string { return "run_agent" }
func (*RunAgentTool) Description() string {
	return "Run a GUI automation task in a browser. Input: a natural-language instruction (include a URL when relevant). Returns bounded executed-action receipts and observations. The summary and done status are not acceptance proof; cross-check actual inputs and later observations before drawing conclusions or repeating steps."
}
func (*RunAgentTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"instruction": map[string]any{"type": "string"}},
		"required":   []string{"instruction"},
	}
}

func (t *RunAgentTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	instruction := fmt.Sprint(args["instruction"])
	emit := agent.EmitFrom(ctx)
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()

	frames := make(chan map[string]any, 64)
	runMsg, _ := json.Marshal(map[string]any{
		"type":        "run",
		"instruction": instruction,
		"session_id":  messages.NewID(),
		"max_steps":   t.MaxSteps,
	})

	opts := []ws.Option{
		ws.WithOnMessage(func(b []byte) {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				select {
				case frames <- m:
				case <-streamCtx.Done():
				}
			}
		}),
	}
	if t.Token != "" {
		opts = append(opts, ws.WithHeaders(http.Header{"Authorization": []string{"Bearer " + t.Token}}))
	}
	cli := ws.NewClient(t.WSURL, opts...)
	cli.Start(streamCtx)
	defer cli.Close()

	// Send queues until the connection is established, then flushes.
	if err := cli.Send(runMsg); err != nil {
		return "", fmt.Errorf("run_agent: send: %w", err)
	}

	var evidence guiEvidence
	drainEvidence := func() {
		// Include frames already received when the deadline wins select.
		// Snapshot the queue length so incoming frames cannot extend the wait.
		for pending := len(frames); pending > 0; pending-- {
			evidence.add(<-frames)
		}
	}
	timeout := time.After(t.Timeout)
	for {
		select {
		case <-ctx.Done():
			stopStream()
			drainEvidence()
			return evidence.result("cancelled", ctx.Err().Error()), ctx.Err()
		case <-timeout:
			stopStream()
			drainEvidence()
			return evidence.result("partial", fmt.Sprintf("GUI timed out after %s; execution may be incomplete.", t.Timeout)), nil
		case m := <-frames:
			switch m["type"] {
			case "done":
				status := "done"
				if m["outcome"] == "partial" {
					status = "partial"
				}
				return evidence.result(status, fmt.Sprint(m["summary"])), nil
			case "error":
				if evidence.actions > 0 {
					return evidence.result("partial", fmt.Sprintf("GUI error: %v", m["error"])), nil
				}
				return evidence.result("error", fmt.Sprint(m["error"])), fmt.Errorf("gui error: %v", m["error"])
			case "call_user":
				surface(emit, "call_user", m)
				return evidence.result("call_user", fmt.Sprint(m["reason"])), nil
			default:
				evidence.add(m)
				surface(emit, fmt.Sprint(m["type"]), m)
			}
		}
	}
}

// describeStep renders one GUI action frame as a short line of progress.
// Non-action frames (screenshots, observations) carry no state change and are
// deliberately ignored — the summary should read as what was DONE.
func describeStep(m map[string]any) string {
	if fmt.Sprint(m["type"]) != "action" {
		return ""
	}
	act, target := fmt.Sprint(m["action"]), fmt.Sprint(m["target"])
	if act == "" || act == "<nil>" {
		return ""
	}
	if target != "" && target != "<nil>" {
		return act + " " + target
	}
	return act
}

func surface(emit func(messages.Message), action string, payload map[string]any) {
	if emit == nil {
		return
	}
	emit(messages.Browser(action, payload, messages.Meta{}))
}
