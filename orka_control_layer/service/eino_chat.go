package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
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

// toEinoMessages converts our chat history into eino schema messages (user /
// assistant turns only; the system prompt is supplied via the agent's
// Instruction, and tool/system events are not replayed into the model input).
func toEinoMessages(msgs []messages.Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Type != messages.EventChat {
			continue
		}
		switch m.Role {
		case messages.RoleUser:
			out = append(out, schema.UserMessage(m.Content))
		case messages.RoleAssistant:
			out = append(out, schema.AssistantMessage(m.Content, nil))
		}
	}
	return out
}

// StreamEinoRun executes the chat on the eino runtime, fanning each Runner event
// into the SAME emit/persist sink (rc.Emit → s.Msg.Deliver) the hand-rolled path
// uses, so persistence and SSE rendering are identical: live EventStream token
// deltas during generation, then the authoritative (persisted) EventChat; tool
// calls correlated with their results into a {tool,args,result} receipt.
func StreamEinoRun(ctx context.Context, rc *agent.RunContext, ag adk.Agent, emit func(messages.Message)) error {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: ag, EnableStreaming: true})
	iter := runner.Run(ctx, toEinoMessages(rc.Messages))

	type pendingCall struct {
		name string
		args map[string]any
	}
	calls := map[string]pendingCall{} // tool_call_id → call info (for args)

	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			return ev.Err
		}
		out := ev.Output
		if out == nil || out.MessageOutput == nil {
			continue
		}
		mv := out.MessageOutput

		var m *schema.Message
		if mv.IsStreaming {
			full, err := drainEinoStream(mv.MessageStream, mv.Role, rc.Meta, emit)
			if err != nil {
				return err
			}
			m = full
		} else {
			m = mv.Message
		}
		if m == nil {
			continue
		}

		switch m.Role {
		case schema.Assistant:
			for _, tc := range m.ToolCalls {
				calls[tc.ID] = pendingCall{name: tc.Function.Name, args: parseJSONArgs(tc.Function.Arguments)}
			}
			if m.Content != "" {
				rc.Messages = append(rc.Messages, messages.Chat(messages.RoleAssistant, m.Content, rc.Meta))
				emit(messages.Chat(messages.RoleAssistant, m.Content, rc.Meta))
			}
		case schema.Tool:
			pc := calls[m.ToolCallID]
			name := pc.name
			if name == "" {
				name = m.ToolName
			}
			payload := map[string]any{"tool": name, "args": pc.args, "result": m.Content}
			emit(messages.Tool("call", payload, rc.Meta))
		}
	}
	return nil
}

// drainEinoStream reads an assistant generation stream, emitting each content
// delta as a live (non-persisted) EventStream frame, and returns the merged
// final message (content + tool calls) for the authoritative persist step.
func drainEinoStream(s *schema.StreamReader[*schema.Message], role schema.RoleType, meta messages.Meta, emit func(messages.Message)) (*schema.Message, error) {
	defer s.Close()
	var chunks []*schema.Message
	var content strings.Builder
	for {
		chunk, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if chunk == nil {
			continue
		}
		if role == schema.Assistant && chunk.Content != "" {
			content.WriteString(chunk.Content)
			emit(messages.StreamDelta(chunk.Content, meta))
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	// ConcatMessages merges deltas + tool-call fragments; on any hiccup fall back
	// to a message carrying just the accumulated text.
	if full, err := schema.ConcatMessages(chunks); err == nil {
		return full, nil
	}
	return &schema.Message{Role: role, Content: content.String()}, nil
}

// runEino is the production entry: it builds an eino agent over the request's
// model + tools + system prompt and streams its events into rc's emit sink.
func (s *ChatService) runEino(ctx context.Context, rc *agent.RunContext, deps PipelineDeps, tools []agent.BaseTool, client llm.Client, model string, _ func(messages.Message)) error {
	instruction := deps.SystemPrompt
	if instruction == "" {
		instruction = middlewares.DefaultSystemPrompt
	}
	ag, err := BuildEinoAgent(ctx, client, model, instruction, tools, 16)
	if err != nil {
		return err
	}
	return StreamEinoRun(ctx, rc, ag, rc.Emit)
}

func parseJSONArgs(s string) map[string]any {
	if s == "" {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) != nil {
		return map[string]any{"_raw": s}
	}
	return m
}
