// Package message_utils centralizes the single emit sink: Deliver decides, per
// message, whether to persist to Mongo and pushes it to the raw SSE sink.
// Keeping this one chokepoint avoids scattered SSE/Mongo writes and message
// drift, and is the natural place to inject trace logging + persist sampling.
package message_utils

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"math/rand"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/db"
)

// Messenger persists messages per policy and forwards them to a raw sink.
type Messenger struct {
	Store    *db.Storage
	Sampling float64 // 0..1; downsamples high-freq tool/browser events
	Log      *slog.Logger
}

// New builds a Messenger.
func New(store *db.Storage, sampling float64, log *slog.Logger) *Messenger {
	if sampling <= 0 {
		sampling = 1.0
	}
	return &Messenger{Store: store, Sampling: sampling, Log: log}
}

// Deliver stamps trace/meta, writes to the raw sink (SSE), and persists when
// policy says so. `raw` is the per-request SSE writer; passing it explicitly
// (instead of going through rc.Emit) avoids recursion when rc.Send is itself a
// closure over Deliver.
func (mu *Messenger) Deliver(rc *agent.RunContext, raw func(messages.Message), m messages.Message, persist bool) {
	// inherit session meta when caller left fields blank
	if m.Meta.ConversationID == "" {
		m.Meta.ConversationID = rc.Meta.ConversationID
	}
	if m.Meta.TaskID == "" {
		m.Meta.TaskID = rc.Meta.TaskID
	}
	if m.Meta.TraceID == "" {
		m.Meta.TraceID = rc.Meta.TraceID
	}
	if m.Meta.UserEmail == "" {
		m.Meta.UserEmail = rc.Meta.UserEmail
	}

	if raw != nil {
		raw(m)
	}

	if mu.shouldPersist(m.Type, persist) && mu.Store != nil {
		row := &db.MessagesTable{
			ID:             m.ID,
			ConversationID: m.Meta.ConversationID,
			Role:           m.Role,
			Content:        m.Content,
			Meta:           m.Meta,
			Action:         m.Action,
			Payload:        stripHeavy(m.Payload), // drop base64 screenshots from DB rows
			Type:           string(m.Type),
			CreatedAt:      m.Timestamp,
		}
		if err := mu.Store.InsertMessage(context.Background(), row); err != nil && mu.Log != nil {
			mu.Log.Error("persist message failed", "trace_id", m.Meta.TraceID, "err", err)
		}
	}

	if mu.Log != nil {
		mu.Log.Debug("emit", "trace_id", m.Meta.TraceID, "type", m.Type, "action", m.Action)
	}
}

// stripHeavy returns a persistence-safe copy of a payload: large inline blobs
// (e.g. base64 screenshots in browser events) are replaced with a marker so
// Mongo rows stay small. The SSE-delivered message keeps the full payload.
func stripHeavy(p any) any {
	mp, ok := p.(map[string]any)
	if !ok {
		return p
	}
	d, ok := mp["data"].(string)
	if !ok || len(d) <= 1024 {
		return p
	}
	cp := make(map[string]any, len(mp))
	maps.Copy(cp, mp)
	cp["data"] = fmt.Sprintf("<omitted %d bytes>", len(d))
	return cp
}

// shouldPersist applies the persistence policy:
//   - heartbeat: never
//   - chat/clarify/task/file: always
//   - tool/browser/agent/skill: only if caller asked AND it passes sampling
func (mu *Messenger) shouldPersist(t messages.EventType, persist bool) bool {
	switch t {
	case messages.EventHeartbeat, messages.EventStream:
		return false
	case messages.EventChat, messages.EventClarify, messages.EventTask, messages.EventFile, messages.EventPlan:
		return true
	case messages.EventTool, messages.EventBrowser, messages.EventAgent, messages.EventSkill:
		return persist && rand.Float64() < mu.Sampling
	default:
		return persist
	}
}
