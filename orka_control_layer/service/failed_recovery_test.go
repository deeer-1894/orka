package service

import (
	"context"
	"errors"
	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/messages"
	"testing"
)

type boundedChargedFailure struct{ spent int }

func (c *boundedChargedFailure) Chat(ctx context.Context, _ llm.Request) (llm.Response, error) {
	b := budgetFrom(ctx)
	b.AddUsage(200, 0)
	c.spent = b.totalSpentTokens()
	return llm.Response{}, errors.New("fixture failure after charged call")
}
func TestBoundedFailedHandoffDoesNotReofferOldAllowance(t *testing.T) {
	client := &boundedChargedFailure{}
	svc, _ := testService(t, client)
	svc.Cfg.Storage.BaseStoragePath = t.TempDir()
	rr := &runResume{Checkpoint: &runCheckpoint{SpentTokens: 700}, Messages: []*schema.Message{schema.UserMessage("continue existing task")}}
	status := svc.Run(context.Background(), ChatRunRequest{Message: "continue existing task", UserEmail: "fixture-user", resumeFrom: rr}, func(messages.Message) {})
	if status != db.RunFailed || rr.SuccessorDurable {
		t.Fatalf("unexpected fixture state: status=%s successor=%v", status, rr.SuccessorDurable)
	}
	if rr.Checkpoint.SpentTokens < client.spent {
		t.Fatalf("failed handoff leaves predecessor offer at %d despite %d cumulative tokens already spent", rr.Checkpoint.SpentTokens, client.spent)
	}
}
