package service

import (
	"context"
	"encoding/json"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"strings"
	"testing"
	"time"
)

func TestFollowupDelegateDurability(t *testing.T) {
	dir := t.TempDir()
	j := newRunJournal(dir, "delegate-ledger", nil)
	b := newRunBudget(100, 10000, 0)
	ctx := withJournal(withBudget(withToolGate(context.Background(), newToolGate()), b), j)
	j.trackState(ctx)
	secondEvent := make(chan struct{})
	model := &gateScriptClient{respond: func(n int, req llm.Request) llm.Response {
		if n == 2 {
			until := time.Now().Add(2 * time.Second)
			for loadJournal(dir, "delegate-ledger") == nil && time.Now().Before(until) {
				time.Sleep(time.Millisecond)
			}
		}
		if n == 3 {
			select {
			case <-secondEvent:
			case <-time.After(2 * time.Second):
				t.Error("second delegate event not observed")
			}
			f := loadJournal(dir, "delegate-ledger")
			if f == nil || f.Checkpoint == nil {
				t.Error("no durable checkpoint")
			} else {
				if f.Checkpoint.SpentTokens != 300 {
					t.Errorf("two delegate tool completions persisted only %d of 300 spent tokens", f.Checkpoint.SpentTokens)
				}
				raw, _ := json.Marshal(f)
				if !strings.Contains(string(raw), "receipt-first") {
					t.Errorf("completed delegate receipt missing at interruption boundary: %s", raw)
				}
			}
		}
		b.AddUsage(100, 0)
		switch n {
		case 0:
			return gateCall("parent-task", "task", `{"subagent_type":"general-purpose","description":"read local data twice"}`)
		case 1:
			return gateCall("receipt-first", "file_read", `{"path":"first.txt"}`)
		case 2:
			return gateCall("receipt-second", "file_read", `{"path":"second.txt"}`)
		default:
			return llm.Response{Content: "finished", FinishReason: "stop"}
		}
	}}
	ag, err := BuildEinoDeepOrchestrator(ctx, model, "main", model, "mini", "delegate work", deepTestTools(), nil, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	rc := &agent.RunContext{Ctx: ctx, Vars: map[string]any{}, Messages: []messages.Message{messages.Chat(messages.RoleUser, "read local data", messages.Meta{})}}
	seen := 0
	if err = StreamEinoRun(ctx, rc, ag, func(m messages.Message) {
		if m.Type == messages.EventTool && m.Action == "call" {
			seen++
			if seen == 2 {
				close(secondEvent)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
}
