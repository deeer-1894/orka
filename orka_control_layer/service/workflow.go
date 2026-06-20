package service

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/db"
)

// RunWorkflow executes a workflow as a DAG in ONE fresh conversation. Steps run
// in dependency order; independent steps in the same wave run concurrently
// (parallelism). A step's RunIf guard can skip it based on earlier outputs, and
// its OnError policy (stop|continue|retry:N) governs failures. Prompts and
// guards may reference a prior step's output with {{step_name}}. A workflow with
// no declared dependencies runs as a linear chain (backward compatible).
func (s *ChatService) RunWorkflow(ctx context.Context, wf db.Workflow, convID string) string {
	if s.Msg == nil || s.Msg.Store == nil || len(wf.Steps) == 0 {
		return ""
	}
	if convID == "" {
		convID = messages.NewID()
	}
	_ = s.Msg.Store.CreateConversation(ctx, &db.ConversationTable{
		ConversationID: convID,
		OwnerEmail:     wf.OwnerEmail,
		Title:          "流程 · " + wf.Name,
		TaskIds:        []string{},
		CreatedAt:      time.Now().UnixMilli(),
	})

	steps := normalizeDAG(wf.Steps)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var mu sync.Mutex
	outputs := map[string]string{} // step name -> captured output
	done := map[string]bool{}      // ran or skipped
	stopped := false

	depsMet := func(st db.WorkflowStep) bool {
		for _, d := range st.DependsOn {
			if !done[d] {
				return false
			}
		}
		return true
	}

	for len(done) < len(steps) {
		// Gather the next wave: every not-done step whose deps are all done.
		mu.Lock()
		if stopped {
			mu.Unlock()
			break
		}
		var wave []db.WorkflowStep
		for _, st := range steps {
			if !done[st.Name] && depsMet(st) {
				wave = append(wave, st)
			}
		}
		mu.Unlock()
		if len(wave) == 0 {
			break // nothing runnable (cycle or all done) — avoid spinning
		}

		var wg sync.WaitGroup
		for _, st := range wave {
			wg.Add(1)
			go func(st db.WorkflowStep) {
				defer wg.Done()
				if ctx.Err() != nil {
					return
				}
				mu.Lock()
				prior := copyMap(outputs)
				mu.Unlock()

				// Conditional guard: skip (but unblock dependents) when false.
				if !evalRunIf(st.RunIf, prior) {
					mu.Lock()
					done[st.Name] = true
					outputs[st.Name] = "(skipped)"
					mu.Unlock()
					return
				}

				out, ok := s.runStep(ctx, wf.OwnerEmail, convID, st, prior)
				mu.Lock()
				outputs[st.Name] = out
				done[st.Name] = true
				if !ok && onErrorStops(st.OnError) {
					stopped = true
					cancel()
				}
				mu.Unlock()
			}(st)
		}
		wg.Wait()
	}
	return convID
}

// runStep runs one step (with its retry policy) and returns (output, ok).
func (s *ChatService) runStep(ctx context.Context, owner, convID string, st db.WorkflowStep, prior map[string]string) (string, bool) {
	prompt := substitute(st.Prompt, prior)
	retries := onErrorRetries(st.OnError)
	var out string
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return out, false
		}
		// Capture the step's answer for {{ref}} substitution and RunIf guards.
		// Prefer the authoritative EventChat; fall back to accumulated streaming
		// deltas if no final chat arrives (tool-heavy turns sometimes stream only).
		var chat, stream strings.Builder
		status := s.Run(ctx, ChatRunRequest{
			Message:        prompt,
			ConversationID: convID,
			UserEmail:      owner,
			Trigger:        "workflow",
		}, func(m messages.Message) {
			if m.Role != messages.RoleAssistant {
				return
			}
			switch m.Type {
			case messages.EventChat:
				chat.WriteString(m.Content)
			case messages.EventStream:
				stream.WriteString(m.Content)
			}
		})
		out = strings.TrimSpace(chat.String())
		if out == "" {
			out = strings.TrimSpace(stream.String())
		}
		if status != db.RunFailed {
			return out, true
		}
		if attempt >= retries || ctx.Err() != nil {
			return out, false
		}
		select {
		case <-ctx.Done():
			return out, false
		case <-time.After(time.Duration(attempt+1) * 3 * time.Second):
		}
	}
}

// normalizeDAG fills implicit dependencies: if no step declares DependsOn, the
// workflow is a linear chain (step i depends on step i-1) — preserving the old
// sequential behavior for existing workflows.
func normalizeDAG(steps []db.WorkflowStep) []db.WorkflowStep {
	for _, st := range steps {
		if len(st.DependsOn) > 0 {
			return steps // explicit DAG
		}
	}
	out := make([]db.WorkflowStep, len(steps))
	for i, st := range steps {
		if i > 0 {
			st.DependsOn = []string{steps[i-1].Name}
		}
		out[i] = st
	}
	return out
}

// evalRunIf evaluates a guard like `research contains FOUND` against prior step
// outputs. Supported ops: contains, !contains, ==, !=. Empty/unparseable guards
// run (fail-open) so a typo never silently drops a step.
func evalRunIf(expr string, outputs map[string]string) bool {
	expr = strings.TrimSpace(substitute(expr, outputs))
	if expr == "" {
		return true
	}
	for _, op := range []string{"!contains", "contains", "==", "!="} {
		i := strings.Index(expr, " "+op+" ")
		if i < 0 {
			continue
		}
		left := strings.TrimSpace(expr[:i])
		right := unquote(strings.TrimSpace(expr[i+len(op)+2:]))
		if v, ok := outputs[left]; ok { // bare step name → its output
			left = v
		}
		switch op {
		case "contains":
			return strings.Contains(left, right)
		case "!contains":
			return !strings.Contains(left, right)
		case "==":
			return strings.TrimSpace(left) == right
		case "!=":
			return strings.TrimSpace(left) != right
		}
	}
	return true
}

// substitute replaces {{step_name}} with that step's output (truncated).
func substitute(s string, outputs map[string]string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	for name, out := range outputs {
		if len(out) > 4000 {
			out = out[:4000]
		}
		s = strings.ReplaceAll(s, "{{"+name+"}}", out)
	}
	return s
}

func onErrorStops(policy string) bool {
	p := strings.ToLower(strings.TrimSpace(policy))
	return p == "" || p == "stop" // default is stop
}

func onErrorRetries(policy string) int {
	p := strings.ToLower(strings.TrimSpace(policy))
	if strings.HasPrefix(p, "retry:") {
		if n, err := strconv.Atoi(strings.TrimSpace(p[len("retry:"):])); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
