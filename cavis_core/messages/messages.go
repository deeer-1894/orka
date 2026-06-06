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
	ConversationID string `json:"conversation_id"`
	TaskID         string `json:"task_id"`
	ModelVersion   string `json:"model_version,omitempty"`
	TraceID        string `json:"trace_id"`             // links one request end-to-end
	UserEmail      string `json:"user_email,omitempty"` // identity for trace/isolation
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
