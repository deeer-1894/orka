// Package trace provides minimal tracing primitives: a TraceID that runs
// through one request (carried in messages.Meta and propagated via context)
// and lightweight spans. SendMsg in the control layer is the single injection
// point that stamps trace info on every emitted event.
package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

type ctxKey int

const traceIDKey ctxKey = iota

// NewTraceID returns a 32-hex-char trace id.
func NewTraceID() string { return randHex(16) }

// NewSpanID returns a 16-hex-char span id.
func NewSpanID() string { return randHex(8) }

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WithTraceID stores a trace id on the context.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

// TraceIDFrom extracts a trace id from the context ("" if absent).
func TraceIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

// Span is a single timed unit of work.
type Span struct {
	TraceID string
	SpanID  string
	Parent  string
	Name    string
	Start   time.Time
	End     time.Time
	Attrs   map[string]any
}

// NewManualSpan creates and starts a lightweight in-process span (not exported
// to OTel). For OTel-exported spans use StartSpan in otel.go.
func NewManualSpan(traceID, parent, name string) *Span {
	return &Span{
		TraceID: traceID,
		SpanID:  NewSpanID(),
		Parent:  parent,
		Name:    name,
		Start:   time.Now(),
		Attrs:   map[string]any{},
	}
}

// Finish stops the span and returns its duration.
func (s *Span) Finish() time.Duration {
	s.End = time.Now()
	return s.End.Sub(s.Start)
}

// Set attaches an attribute.
func (s *Span) Set(k string, v any) { s.Attrs[k] = v }
