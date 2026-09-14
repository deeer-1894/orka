package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
)

// Auto is an ordered-list default, independent of prompt complexity and cycle
// count. Legacy deployment mini configuration must never activate routing.
func TestAutoNeverEscalates(t *testing.T) {
	for _, prompt := range []string{"hello", "compare and research this topic step by step"} {
		t.Run(prompt, func(t *testing.T) {
			calls := 0
			primary := &gateScriptClient{respond: func(n int, req llm.Request) llm.Response {
				calls++
				if req.Model != "first" {
					t.Errorf("switched to %s", req.Model)
				}
				if n < 4 {
					return gateCall(fmt.Sprintf("read-%d", n), "file_read", fmt.Sprintf(`{"path":"file-%d.txt"}`, n))
				}
				return llm.Response{Content: "done", FinishReason: "stop"}
			}}
			svc, _ := testService(t, primary)
			svc.Cfg.LLM.Model = "first"
			svc.Cfg.LLM.MiniModel = "legacy-fast"
			svc.Cfg.LLM.Models = []string{"manual"}
			obsolete := llm.NewMock(llm.Response{Content: "wrong client", FinishReason: "stop"})
			svc.Mini = obsolete
			svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
				return deepTestTools(), nil, nil
			}
			status := svc.Run(context.Background(), ChatRunRequest{ConversationID: "conv", Message: prompt, SelectedVersion: ModelAuto}, (&collector{}).sink)
			if status != db.RunDone || calls != 5 || len(obsolete.Requests) != 0 {
				t.Fatalf("status=%s calls=%d obsolete=%d", status, calls, len(obsolete.Requests))
			}
		})
	}
}

func TestDeploymentModelListDropsMiniTier(t *testing.T) {
	svc, _ := testService(t, llm.NewMock())
	svc.Cfg.LLM.Model = "first"
	svc.Cfg.LLM.MiniModel = "obsolete"
	svc.Cfg.LLM.Models = []string{"manual", "first", "mini"}
	cfg, err := svc.ModelConfigForUser("")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(cfg.Models) != "[first manual mini]" || cfg.Model != "first" || cfg.MiniModel != "first" {
		t.Fatal(cfg.Models, cfg.Model, cfg.MiniModel)
	}
	for _, tc := range []struct{ selection, want string }{{"", "first"}, {"auto", "first"}, {"manual", "manual"}, {"mini", "mini"}, {"obsolete", "first"}} {
		client, name := svc.modelFor(tc.selection)
		if client != svc.Main || name != tc.want {
			t.Errorf("selection %s: %s", tc.selection, name)
		}
	}
}
