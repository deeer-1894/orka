// Package connectors holds adapters to external executors. RunAgentTool wraps
// the remote GUI agent as a BaseTool: it opens a WebSocket to GUI_AGENT_WS_URL,
// drives one task, surfaces browser events into the run's SSE stream (via the
// emit side-channel on the context), and returns the final summary.
package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cavis-oss/cavis_core/agent"
	"github.com/cavis-oss/cavis_core/messages"
	"github.com/cavis-oss/cavis_core/ws"
)

// RunAgentTool adapts the GUI executor to agent.BaseTool.
type RunAgentTool struct {
	WSURL    string
	MaxSteps int
	Timeout  time.Duration
}

// NewRunAgentTool builds a run_agent tool targeting wsURL.
func NewRunAgentTool(wsURL string) *RunAgentTool {
	return &RunAgentTool{WSURL: wsURL, MaxSteps: 8, Timeout: 120 * time.Second}
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

	cli := ws.NewClient(t.WSURL,
		ws.WithOnMessage(func(b []byte) {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				select {
				case frames <- m:
				default:
				}
			}
		}),
	)
	cli.Start(ctx)
	defer cli.Close()

	// Send queues until the connection is established, then flushes.
	if err := cli.Send(runMsg); err != nil {
		return "", fmt.Errorf("run_agent: send: %w", err)
	}

	timeout := time.After(t.Timeout)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout:
			return "", fmt.Errorf("run_agent: timed out after %s", t.Timeout)
		case m := <-frames:
			switch m["type"] {
			case "done":
				return fmt.Sprintf("GUI task completed: %v", m["summary"]), nil
			case "error":
				return "", fmt.Errorf("gui error: %v", m["error"])
			case "call_user":
				surface(emit, "call_user", m)
				return fmt.Sprintf("GUI needs user input: %v", m["reason"]), nil
			default:
				surface(emit, fmt.Sprint(m["type"]), m)
			}
		}
	}
}

func surface(emit func(messages.Message), action string, payload map[string]any) {
	if emit == nil {
		return
	}
	emit(messages.Browser(action, payload, messages.Meta{}))
}
