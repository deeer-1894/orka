// Package messages defines the cross-module event protocol. The Message
// envelope is the shared "language" between control layer, tool layer,
// executor and frontend.
package messages

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type EventType string

const (
	EventTask      EventType = "task"      // task status change
	EventChat      EventType = "chat"      // conversational message
	EventClarify   EventType = "clarify"   // interrupt question
	EventFile      EventType = "file"      // file op result
	EventSkill     EventType = "skill"     // skill invocation
	EventAgent     EventType = "agent"     // agent process event
	EventTool      EventType = "tool"      // tool call event
	EventBrowser   EventType = "browser"   // GUI process event
	EventHeartbeat EventType = "heartbeat" // keep-alive
	EventStream    EventType = "stream"    // streaming token delta (not persisted)
	EventConfirm   EventType = "confirm"   // approval gate before a risky tool runs
	EventPlan      EventType = "plan"      // the agent's task checklist + live progress
)

// Roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleSystem    = "system"
)

// Meta is carried on every message for routing, persistence and tracing.
type Meta struct {
	ConversationID string `json:"conversation_id" bson:"conversation_id"`
	TaskID         string `json:"task_id" bson:"task_id"`
	ModelVersion   string `json:"model_version,omitempty" bson:"model_version,omitempty"`
	TraceID        string `json:"trace_id" bson:"trace_id"`                             // links one request end-to-end
	UserEmail      string `json:"user_email,omitempty" bson:"user_email,omitempty"`     // identity for trace/isolation
	AgentID        string `json:"agent_id,omitempty" bson:"agent_id,omitempty"`         // which (sub-)agent emitted this; "" = orchestrator
	ParentAgentID  string `json:"parent_agent_id,omitempty" bson:"parent_agent_id,omitempty"` // the agent that delegated to AgentID
}

// Message is the unified cross-module envelope.
type Message struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	Role      string    `json:"role"`
	Content   string    `json:"content,omitempty"`
	Action    string    `json:"action,omitempty"`
	Payload   any       `json:"payload,omitempty"`
	Meta      Meta      `json:"meta"`
	Timestamp int64     `json:"ts"`
}

// ClarifyMessage is the payload of an EventClarify message.
type ClarifyMessage struct {
	Question  string   `json:"question"`
	Options   []string `json:"options,omitempty"`
	Context   string   `json:"context,omitempty"`
	ResumeKey string   `json:"resume_key"` // corresponds to the checkpoint key
}

// NewID returns a short random hex id.
func NewID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// New builds a base message with id and timestamp filled in.
func New(t EventType, role string, meta Meta) Message {
	return Message{ID: NewID(), Type: t, Role: role, Meta: meta, Timestamp: time.Now().UnixMilli()}
}

// Chat builds a conversational message.
func Chat(role, content string, meta Meta) Message {
	m := New(EventChat, role, meta)
	m.Content = content
	return m
}

// StreamDelta builds an assistant streaming token delta. These are emitted over
// SSE for live rendering and are never persisted; the final EventChat carries
// the full, authoritative message.
func StreamDelta(content string, meta Meta) Message {
	m := New(EventStream, RoleAssistant, meta)
	m.Content = content
	return m
}

// StreamReset tells the UI to discard the transient streaming bubble built so
// far. Emitted when a model call is retried or failed over mid-stream: the
// partial text from the failed attempt must not be concatenated with the new
// attempt's output. Not persisted (same as other stream frames).
func StreamReset(meta Meta) Message {
	m := New(EventStream, RoleAssistant, meta)
	m.Action = "reset"
	return m
}

// ReasoningDelta is a live "thinking" token delta from a reasoning model, shown
// in a collapsible indicator (not the answer, not persisted). Distinguished from
// answer deltas by Action == "reasoning".
func ReasoningDelta(content string, meta Meta) Message {
	m := New(EventStream, RoleAssistant, meta)
	m.Action = "reasoning"
	m.Content = content
	return m
}


// Tool builds a tool-event message.
func Tool(action string, payload any, meta Meta) Message {
	m := New(EventTool, RoleTool, meta)
	m.Action = action
	m.Payload = payload
	return m
}

// Clarify builds an interrupt-question message.
func Clarify(c ClarifyMessage, meta Meta) Message {
	m := New(EventClarify, RoleAssistant, meta)
	m.Payload = c
	return m
}

// ConfirmRequest is the payload of an EventConfirm message: the agent is about
// to run a side-effecting tool and is waiting for the user to approve or reject.
type ConfirmRequest struct {
	ID      string `json:"id"`      // resolve via /chat/confirm
	Tool    string `json:"tool"`    // tool name (e.g. shell, run_agent)
	Summary string `json:"summary"` // human-readable action (the command / instruction / URL)
}

// Confirm builds an approval-gate message.
func Confirm(c ConfirmRequest, meta Meta) Message {
	m := New(EventConfirm, RoleAssistant, meta)
	m.Payload = c
	return m
}

// PlanStep is one item in the agent's declared task checklist. Status is one of
// "pending" | "active" | "done" so the UI can show real per-step progress
// instead of an all-or-nothing list.
type PlanStep struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

// PlanUpdate is the payload of an EventPlan message: the agent's current plan and
// the progress of each step. The agent re-emits the whole plan on every update
// (idempotent snapshot), and the UI renders the latest one.
type PlanUpdate struct {
	Steps []PlanStep `json:"steps"`
}

// Plan builds a plan/checklist message.
func Plan(p PlanUpdate, meta Meta) Message {
	m := New(EventPlan, RoleAssistant, meta)
	m.Payload = p
	return m
}

// Task builds a task status message (action: start/running/done/failed/paused).
func Task(action string, meta Meta) Message {
	m := New(EventTask, RoleSystem, meta)
	m.Action = action
	return m
}

// Browser builds a GUI-process message.
func Browser(action string, payload any, meta Meta) Message {
	m := New(EventBrowser, RoleSystem, meta)
	m.Action = action
	m.Payload = payload
	return m
}

// Heartbeat builds a keep-alive message.
func Heartbeat(meta Meta) Message { return New(EventHeartbeat, RoleSystem, meta) }

// JSON returns the compact JSON encoding.
func (m Message) JSON() ([]byte, error) { return json.Marshal(m) }

// SSE formats the message as an SSE "data:" frame terminated by a blank line.
func (m Message) SSE() ([]byte, error) {
	b, err := m.JSON()
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(b)+8)
	out = append(out, "data: "...)
	out = append(out, b...)
	out = append(out, '\n', '\n')
	return out, nil
}
