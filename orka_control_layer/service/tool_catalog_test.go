package service

import (
	"context"
	"os"
	"testing"

	"github.com/orka-oss/orka_core/agent"
)

func TestToolCatalogDoesNotCreateOrAuthorizeAConversation(t *testing.T) {
	base := t.TempDir()
	svc := &ChatService{ToolsFor: LocalToolsProvider(base)}
	catalog := svc.ToolCatalog(context.Background(), "owner")
	fileTool := false
	for _, tool := range catalog {
		if tool.Name == "file_write" {
			fileTool = true
		}
	}
	if !fileTool {
		t.Fatal("new-chat picker lost file tools")
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatal("catalog created a synthetic session")
	}
	if _, _, err := svc.ToolsFor(context.Background(), ChatRunRequest{UserEmail: "owner"}); err == nil {
		t.Fatal("normal execution allowed missing session")
	}
	svc.ToolsFor = func(ctx context.Context, req ChatRunRequest) ([]agent.BaseTool, func(), error) {
		if !catalogOnly(ctx) || requestedExecutionScope(ctx) || req.ConversationID != "" {
			t.Fatal("catalog context carries execution authority")
		}
		return nil, nil, nil
	}
	svc.ToolCatalog(context.Background(), "owner")
}
func TestExecutionIterationLimitUsesTaskPolicy(t *testing.T) {
	if got := executionIterationLimit(withBudget(context.Background(), newRunBudget(450, 2000000, runMaxWall))); got != 450 {
		t.Fatal(got)
	}
	if got := executionIterationLimit(context.Background()); got != einoMaxIters {
		t.Fatal(got)
	}
}
