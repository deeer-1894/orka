package service

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_control_layer/llm"
)

// This file is the Phase-1 foundation of the Eino-library migration: it stands
// up a real eino adk.ChatModelAgent + Runner backed by our existing llm.Client
// (via llm.EinoModel) and tool suite (via EinoTools), running in parallel to the
// hand-rolled runner and gated by config so the working path is untouched.

// BuildEinoAgent constructs an eino ReAct ChatModelAgent over our model + tools.
func BuildEinoAgent(ctx context.Context, client llm.Client, model, instruction string, tools []agent.BaseTool, maxIters int) (adk.Agent, error) {
	if maxIters <= 0 {
		maxIters = 16
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "orka",
		Description: "Orka assistant",
		Instruction: instruction,
		Model:       llm.NewEinoModel(client, model),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: EinoTools(tools)},
		},
		MaxIterations: maxIters,
	})
}

// RunEinoOnce runs the agent to completion on a single user message and returns
// the final assistant text. Tool steps and intermediate assistant turns are
// drained; only the last assistant message content is returned. (Phase 2 will
// fan these events out to SSE instead of collapsing them.)
func RunEinoOnce(ctx context.Context, ag adk.Agent, userMessage string) (string, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: ag})
	iter := runner.Query(ctx, userMessage)
	var final string
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			return "", ev.Err
		}
		out := ev.Output
		if out == nil || out.MessageOutput == nil || out.MessageOutput.IsStreaming {
			continue
		}
		if m := out.MessageOutput.Message; m != nil && m.Role == schema.Assistant && m.Content != "" {
			final = m.Content
		}
	}
	return final, nil
}
