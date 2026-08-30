// Package connectors holds adapters to external executors. RunAgentTool wraps
// the remote GUI agent as a BaseTool: it opens a WebSocket to GUI_AGENT_WS_URL,
// drives one task, surfaces browser events into the run's SSE stream (via the
// emit side-channel on the context), and returns the final summary.
package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	return "Run a GUI automation task in a browser. Input: a natural-language instruction (include a URL when relevant)."
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
				default:
				}
			}
		}),
	}
	if t.Token != "" {
		opts = append(opts, ws.WithHeaders(http.Header{"Authorization": []string{"Bearer " + t.Token}}))
	}
	cli := ws.NewClient(t.WSURL, opts...)
	cli.Start(ctx)
	defer cli.Close()

	// Send queues until the connection is established, then flushes.
	if err := cli.Send(runMsg); err != nil {
		return "", fmt.Errorf("run_agent: send: %w", err)
	}

	// Remember the actions taken. A GUI run that times out at step 8 of 10 has
	// really done those eight things — the browser state reflects them — and
	// returning a bare error threw that away, leaving the orchestrator unable to
	// tell "nothing happened" from "most of it happened". Measured here, 29 of
	// 292 run_agent calls timed out, every one discarding its progress.
	var done []string
	timeout := time.After(t.Timeout)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout:
			return partialResult(t.Timeout, done), nil
		case m := <-frames:
			switch m["type"] {
			case "done":
				return fmt.Sprintf("GUI task completed: %v", m["summary"]), nil
			case "error":
				// Report the steps that DID land alongside the failure, for the
				// same reason: the orchestrator's next move depends on how far
				// this got, not only on the fact that it stopped.
				if len(done) > 0 {
					return "", fmt.Errorf("gui error after %d step(s) (%s): %v",
						len(done), strings.Join(done, "; "), m["error"])
				}
				return "", fmt.Errorf("gui error: %v", m["error"])
			case "call_user":
				surface(emit, "call_user", m)
				return fmt.Sprintf("GUI needs user input: %v", m["reason"]), nil
			default:
				if step := describeStep(m); step != "" {
					done = append(done, step)
				}
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

// partialResult reports a timeout as an OUTCOME rather than an error, so the
// orchestrator can decide whether to continue, retry the remainder, or accept
// what was achieved. Returned as a normal result for the same reason tool
// failures are: an error here aborts the whole eino node.
func partialResult(after time.Duration, done []string) string {
	if len(done) == 0 {
		return fmt.Sprintf("GUI task timed out after %s with no completed steps. "+
			"Nothing was changed; retry with a narrower instruction or use another tool.", after)
	}
	return fmt.Sprintf("GUI task INCOMPLETE — timed out after %s having completed %d step(s): %s. "+
		"These steps already took effect, so do not repeat them; continue from this state or report what is missing.",
		after, len(done), strings.Join(done, "; "))
}

func surface(emit func(messages.Message), action string, payload map[string]any) {
	if emit == nil {
		return
	}
	emit(messages.Browser(action, payload, messages.Meta{}))
}
