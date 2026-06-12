package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/orka-oss/orka_core/messages"
)

// metaKey carries the current run's Meta on the context so a sub-agent (invoked
// as a tool, which only receives ctx+args) can inherit conversation/trace/user
// identity and learn its parent's AgentID.
type metaKey struct{}

// WithMeta attaches run metadata to ctx.
func WithMeta(ctx context.Context, m messages.Meta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

// MetaFrom returns the run metadata from ctx (zero value if absent).
func MetaFrom(ctx context.Context) messages.Meta {
	if m, ok := ctx.Value(metaKey{}).(messages.Meta); ok {
		return m
	}
	return messages.Meta{}
}

// AgentSpec describes a sub-agent exposed to an orchestrator as a tool. Following
// Eino's host/supervisor shape, the Name + Description drive the orchestrator's
// delegation decision (the model "calls" the agent like any other tool).
type AgentSpec struct {
	Name        string
	Description string
}

// AgentTool wraps a sub-agent pipeline as a BaseTool. The orchestrator delegates
// by calling it; the sub-agent then runs in an ISOLATED RunContext (its own
// Vars/Messages — so it can burn many tool iterations without polluting the
// orchestrator's context) but emits its events into the PARENT's SSE sink
// (tagged with its AgentID so the UI can group them) and returns a compact
// STRUCTURED result rather than its full transcript.
type AgentTool struct {
	Spec  AgentSpec
	Tools []BaseTool                    // the sub-agent's tool subset (RBAC-scoped)
	Build func(tools []BaseTool) Runner // builds the sub-agent's pipeline
	Final func(*RunContext) string      // extracts the sub-agent's final answer
}

// asString renders a tool arg as a string, treating a missing (nil) value as
// empty rather than the literal "<nil>".
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func (a *AgentTool) Name() string        { return a.Spec.Name }
func (a *AgentTool) Description() string  { return a.Spec.Description }
func (a *AgentTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "A self-contained task brief for this agent: what to do and what to return.",
			},
		},
		"required": []string{"task"},
	}
}

// Invoke runs the sub-agent on the given task brief and returns its structured
// result. It is safe to run concurrently (the orchestrator's tool batch may
// dispatch several sub-agents at once).
func (a *AgentTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	brief := strings.TrimSpace(asString(args["task"]))
	if brief == "" {
		brief = strings.TrimSpace(asString(args["input"]))
	}
	if brief == "" {
		return "", fmt.Errorf("agent %q: empty task brief", a.Spec.Name)
	}

	parent := MetaFrom(ctx)
	child := parent
	child.ParentAgentID = parent.AgentID
	child.AgentID = a.Spec.Name

	// Events bubble into the parent's sink; meta on ctx becomes the child's so a
	// nested sub-agent inherits correctly.
	emit := EmitFrom(ctx)
	childCtx := WithEmit(WithMeta(ctx, child), emit)

	rc := &RunContext{
		Ctx:      childCtx,
		Vars:     map[string]any{}, // isolated from the orchestrator
		Tools:    a.Tools,
		Meta:     child,
		Send:     emit,
		Messages: []messages.Message{messages.Chat(messages.RoleUser, brief, child)},
	}

	if err := a.Build(a.Tools).Run(rc); err != nil {
		return "", fmt.Errorf("agent %q: %w", a.Spec.Name, err)
	}
	out := ""
	if a.Final != nil {
		out = strings.TrimSpace(a.Final(rc))
	}
	if out == "" {
		out = fmt.Sprintf("(agent %q finished with no explicit result)", a.Spec.Name)
	}
	return out, nil
}
