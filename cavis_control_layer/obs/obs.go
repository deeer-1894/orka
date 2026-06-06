// Package obs provides structured logging and lightweight runtime metrics.
package obs

import (
	"log/slog"
	"os"
	"sync/atomic"
)

// NewLogger returns a JSON slog logger at the given level.
func NewLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}

// Metrics holds simple runtime counters/gauges. Phase 7 exposes them.
type Metrics struct {
	ActiveSessions atomic.Int64 // currently running chat sessions
	Checkpoints    atomic.Int64 // live checkpoints
	ToolCalls      atomic.Int64 // total tool invocations
	ToolCallNanos  atomic.Int64 // cumulative tool wall time (ns)
}

// NewMetrics returns a zeroed Metrics.
func NewMetrics() *Metrics { return &Metrics{} }

// ObserveToolCall records one tool invocation taking durNanos.
func (m *Metrics) ObserveToolCall(durNanos int64) {
	m.ToolCalls.Add(1)
	m.ToolCallNanos.Add(durNanos)
}

// Snapshot is a point-in-time view of metrics.
type Snapshot struct {
	ActiveSessions   int64   `json:"active_sessions"`
	Checkpoints      int64   `json:"checkpoints"`
	ToolCalls        int64   `json:"tool_calls"`
	AvgToolCallMicro float64 `json:"avg_tool_call_micros"`
}

// Snapshot reads current metric values.
func (m *Metrics) Snapshot() Snapshot {
	calls := m.ToolCalls.Load()
	var avg float64
	if calls > 0 {
		avg = float64(m.ToolCallNanos.Load()) / float64(calls) / 1000.0
	}
	return Snapshot{
		ActiveSessions:   m.ActiveSessions.Load(),
		Checkpoints:      m.Checkpoints.Load(),
		ToolCalls:        calls,
		AvgToolCallMicro: avg,
	}
}
