// Package connectors holds adapters to external executors. RunAgentTool wraps
// the remote GUI agent as a BaseTool: it opens a WebSocket to GUI_AGENT_WS_URL,
// drives one task, surfaces browser events into the run's SSE stream (via the
// emit side-channel on the context), and returns a bounded execution evidence window alongside the final summary.
package connectors

import (
	"context"
	"fmt"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// RunAgentTool adapts the GUI executor to agent.BaseTool.
type RunAgentTool struct {
	WSURL        string
	Token        string // shared secret sent as Authorization: Bearer on the WS handshake
	MaxSteps     int
	Timeout      time.Duration // execution budget, starts after the queued phase
	QueueTimeout time.Duration
}

// NewRunAgentTool builds a run_agent tool targeting a private wsURL. A shared
// bearer token is required to authenticate the control-layer caller.
// MaxSteps bounds one browser invocation; the GUI agent also stops early on
// no-progress (repeated actions) and returns a grounded page snapshot, so a
// flailing run ends in a few steps rather than burning the whole budget.
func NewRunAgentTool(wsURL, token string) *RunAgentTool {
	return &RunAgentTool{WSURL: wsURL, Token: token, MaxSteps: 10, Timeout: 90 * time.Second, QueueTimeout: 30 * time.Second}
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

func (t *RunAgentTool) Invoke(ctx context.Context, args map[string]any) (result string, invokeErr error) {
	identity, modelConfig, err := guiContext(ctx)
	if err != nil {
		return "", err
	}
	instruction, err := guiInstruction(args)
	if err != nil {
		return "", err
	}
	queueTimeout, executionTimeout := t.QueueTimeout, t.Timeout
	if queueTimeout <= 0 {
		queueTimeout = 30 * time.Second
	}
	if executionTimeout <= 0 {
		executionTimeout = 90 * time.Second
	}
	if queueTimeout > 600*time.Second || executionTimeout > 600*time.Second || t.MaxSteps < 1 || t.MaxSteps > 100 {
		return "", fmt.Errorf("GUI timeouts must be at most 600 seconds and max_steps between 1 and 100")
	}
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	queueCtx, cancelDial := context.WithTimeout(streamCtx, queueTimeout)
	defer cancelDial()
	connection, err := dialGUI(queueCtx, t.WSURL, t.Token)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	invocationID := messages.NewID()
	usage := &guiUsageLedger{}
	callCtx, finish, err := llm.BeginExternalCall(ctx, llm.ExternalCallSpec{
		CallID: invocationID, Source: "gui", MaxSteps: t.MaxSteps,
		PromptTokens: 32768, MaxCompletionTokens: modelConfig.Policy.MaxTokens,
	})
	if err != nil {
		return "", err
	}
	defer func() {
		// Stop remote execution before detached accounting can wait on storage.
		stopStream()
		connection.Close()
		invokeErr = finish(ctx, usage.complete && usage.unknown == 0 && usage.calls > 0, invokeErr)
	}()
	request := map[string]any{"type": "run", "instruction": instruction, "session_id": invocationID,
		"identity": identity, "model_config": guiWireConfig(modelConfig), "max_steps": t.MaxSteps,
		"queue_timeout": queueTimeout.Seconds(), "execution_timeout": executionTimeout.Seconds()}
	deadline, _ := queueCtx.Deadline()
	connection.SetWriteDeadline(deadline)
	if err := connection.WriteJSON(request); err != nil {
		return "", fmt.Errorf("GUI request send failed")
	}
	request = nil // do not retain credential-bearing payloads in frame/evidence state
	frames := make(chan map[string]any, 64)
	matches := func(frame map[string]any) bool {
		return frame["session_id"] == invocationID && frame["run_id"] == identity.RunID
	}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			var frame map[string]any
			err := connection.ReadJSON(&frame)
			if err != nil {
				frame = map[string]any{"type": "transport_error", "session_id": invocationID, "run_id": identity.RunID}
			}
			if !matches(frame) {
				continue
			}
			select {
			case frames <- frame:
			case <-streamCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	stopReader := func() {
		stopStream()
		connection.Close()
		<-readerDone
	}
	defer stopReader()
	meta := agent.MetaFrom(ctx)
	meta.UserEmail, meta.ConversationID, meta.RunID = identity.OwnerID, identity.ConversationID, identity.RunID
	evidence := guiEvidence{usage: usage, phase: "queue"}
	collect := func(frame map[string]any) {
		if !matches(frame) {
			return
		}
		evidence.add(frame)
		usage.add(callCtx, frame)
	}
	drain := func() {
		// Join the reader before draining. Keep only this invocation's
		// evidence/usage; buffered screenshots are discarded without SSE.
		stopReader()
		for remaining := len(frames); remaining > 0; remaining-- {
			collect(<-frames)
		}
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		// A ready buffered frame must not win a select against cancellation.
		if ctx.Err() != nil {
			drain()
			return evidence.result("cancelled", ctx.Err().Error()), ctx.Err()
		}
		select {
		case <-ctx.Done():
			stopStream()
			drain()
			return evidence.result("cancelled", ctx.Err().Error()), ctx.Err()
		case <-timer.C:
			stopStream()
			drain()
			return evidence.result("partial", "GUI "+evidence.phase+" timeout; inspect current state before retrying"), nil
		case frame := <-frames:
			if ctx.Err() != nil {
				collect(frame) // retain receipts/usage, never resurface old events
				continue
			}
			switch frame["type"] {
			case "started":
				if evidence.phase == "queue" {
					evidence.phase = "execution"
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					// The server owns the precise execution deadline. Give its
					// terminal evidence a small bounded transport grace period.
					timer.Reset(executionTimeout + time.Second)
				}
				surface(ctx, meta, "started", frame)
			case "done", "error", "call_user":
				collect(frame)
				usage.finish(frame)
				if phase, ok := frame["phase"].(string); ok {
					evidence.phase = guiClip(phase, 32)
				}
				if frame["type"] == "call_user" {
					surface(ctx, meta, "call_user", frame)
					return evidence.result("call_user", fmt.Sprint(frame["reason"])), nil
				}
				if frame["type"] == "error" {
					return evidence.result("partial", fmt.Sprint(frame["error"])), nil
				}
				status := "done"
				if frame["outcome"] == "partial" {
					status = "partial"
				}
				return evidence.result(status, fmt.Sprint(frame["summary"])), nil
			case "transport_error":
				return evidence.result("partial", "GUI connection closed before its terminal outcome"), nil
			case "action", "observe", "usage", "screenshot", "queued", "session", "progress":
				collect(frame)
				surface(ctx, meta, fmt.Sprint(frame["type"]), frame)
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

func surface(ctx context.Context, meta messages.Meta, action string, payload map[string]any) {
	emit := agent.EmitFrom(ctx)
	if emit == nil || ctx.Err() != nil {
		return
	}
	emit(messages.Browser(action, payload, meta))
}
