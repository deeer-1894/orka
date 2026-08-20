package service

import (
	"context"
	"sync"
	"time"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

type capturedSalesBIAssist struct {
	toolName string
	args     map[string]any
	raw      string
}

type salesBIAssistCapture struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	value  *capturedSalesBIAssist
}

type salesBIAssistCaptureContextKey struct{}

func withSalesBIAssistCapture(ctx context.Context, capture *salesBIAssistCapture) context.Context {
	return context.WithValue(ctx, salesBIAssistCaptureContextKey{}, capture)
}

func captureSalesBIAssist(ctx context.Context, toolName string, args map[string]any, raw string) bool {
	if _, _, ok := assistPendingFromResult(raw, time.Now()); !ok {
		return false
	}
	capture, _ := ctx.Value(salesBIAssistCaptureContextKey{}).(*salesBIAssistCapture)
	if capture == nil {
		return false
	}
	capture.mu.Lock()
	if capture.value == nil {
		capture.value = &capturedSalesBIAssist{toolName: toolName, args: args, raw: raw}
	}
	cancel := capture.cancel
	capture.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

func (c *salesBIAssistCapture) take() (*capturedSalesBIAssist, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value == nil {
		return nil, false
	}
	value := c.value
	c.value = nil
	return value, true
}

func pauseForSalesBIAssist(rc *agent.RunContext, raw string, meta messages.Meta) bool {
	pending, prompt, ok := assistPendingFromResult(raw, time.Now())
	if !ok {
		return false
	}
	rc.Put(salesBIAssistKey, pending)
	rc.Messages = append(rc.Messages, messages.Chat(messages.RoleAssistant, prompt, meta))
	rc.Interrupt = &agent.Interrupt{Reason: "clarify", Clarify: &messages.ClarifyMessage{Question: prompt}}
	return true
}
