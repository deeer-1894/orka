package llm

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// EinoModel adapts our llm.Client to eino's model.ToolCallingChatModel, so the
// existing, proven DeepSeek client (including the retry decorator) backs an eino
// ChatModelAgent. This lets the Eino migration swap the RUNTIME (runner, agent,
// middlewares) without simultaneously swapping the HTTP/model layer — one
// variable at a time. A later phase may replace this with eino-ext's native
// openai provider.
type EinoModel struct {
	client Client
	model  string
	tools  []*schema.ToolInfo // bound via WithTools; nil until bound
	agent  string             // who this instance belongs to, for call attribution
}

// NewEinoModel wraps c as an eino ToolCallingChatModel for the given model name.
func NewEinoModel(c Client, modelName string) *EinoModel {
	return &EinoModel{client: c, model: modelName}
}

// ForAgent labels an instance with the agent that owns it, so timing can be
// attributed. This is the only place the answer is known: eino calls the model
// from inside its graph with a context we never construct, and every concurrent
// delegate of a given kind shares one name, so nothing downstream can tell a
// delegate's call from the orchestrator's. Each agent builds its own EinoModel,
// which makes the instance itself the attribution.
func (m *EinoModel) ForAgent(name string) *EinoModel {
	cp := *m
	cp.agent = name
	return &cp
}

var _ model.ToolCallingChatModel = (*EinoModel)(nil)

// WithTools returns a NEW instance bound to the given tools (concurrency-safe,
// as the eino contract requires — never mutates the receiver).
func (m *EinoModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.tools = tools
	return &cp, nil
}

// Generate runs a single completion.
func (m *EinoModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	resp, err := m.client.Chat(withAgent(ctx, m.agent), m.request(input, opts))
	if err != nil {
		return nil, err
	}
	return fromResponse(resp), nil
}

// Stream streams the completion. When the underlying client supports streaming
// we forward token deltas; otherwise we fall back to a single-chunk stream over
// Generate (eino consumers treat both uniformly).
func (m *EinoModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sc, ok := m.client.(StreamingClient)
	if !ok {
		out, err := m.Generate(ctx, input, opts...)
		if err != nil {
			return nil, err
		}
		return schema.StreamReaderFromArray([]*schema.Message{out}), nil
	}

	req := m.request(input, opts)
	sr, sw := schema.Pipe[*schema.Message](8)
	go func() {
		defer sw.Close()
		streamedContent := false
		resp, err := sc.ChatStream(withAgent(ctx, m.agent), req, func(delta string) {
			if delta != "" {
				streamedContent = true
				sw.Send(&schema.Message{Role: schema.Assistant, Content: delta}, nil)
			}
		})
		if err != nil {
			sw.Send(nil, err)
			return
		}
		// Final chunk carries tool calls + usage. If content was already streamed
		// as deltas above, blank it here to avoid duplication. But a reasoning
		// model (e.g. MiMo) may emit only reasoning_content deltas and no content
		// deltas — in that case the client falls content back to the reasoning, so
		// keep it on the final chunk or eino's summarizer sees an empty message.
		final := fromResponse(resp)
		if streamedContent {
			final.Content = ""
		}
		sw.Send(final, nil)
	}()
	return sr, nil
}

// request converts eino messages + tools into our wire Request. Tools may come
// either from the WithTools METHOD (m.tools) or, as eino's ChatModelAgent
// actually does, from a request-time model.WithTools OPTION — the latter wins.
func (m *EinoModel) request(input []*schema.Message, opts []model.Option) Request {
	tools := m.tools
	if co := model.GetCommonOptions(&model.Options{}, opts...); len(co.Tools) > 0 {
		tools = co.Tools
	}
	req := Request{Model: m.model, Messages: toChatMessages(input)}
	for _, ti := range tools {
		req.Tools = append(req.Tools, toToolSpec(ti))
	}
	return req
}

// ---- conversions ----

func toChatMessages(msgs []*schema.Message) []ChatMessage {
	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		cm := ChatMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		out = append(out, cm)
	}
	return out
}

func fromResponse(resp Response) *schema.Message {
	msg := &schema.Message{
		Role:             schema.Assistant,
		Content:          resp.Content,
		ReasoningContent: resp.Reasoning,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: resp.FinishReason,
			Usage: &schema.TokenUsage{
				PromptTokens:     resp.Usage.PromptTokens,
				CompletionTokens: resp.Usage.CompletionTokens,
				TotalTokens:      resp.Usage.TotalTokens,
			},
		},
	}
	for i, tc := range resp.ToolCalls {
		idx := i
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			Index:    &idx,
			ID:       tc.ID,
			Type:     "function",
			Function: schema.FunctionCall{Name: tc.Name, Arguments: tc.Arguments},
		})
	}
	return msg
}

// toToolSpec converts an eino ToolInfo back to our wire ToolSpec (the model
// adapter is the boundary, so it owns both directions of the tool schema).
func toToolSpec(ti *schema.ToolInfo) ToolSpec {
	spec := ToolSpec{Name: ti.Name, Description: ti.Desc}
	if ti.ParamsOneOf != nil {
		if js, err := ti.ParamsOneOf.ToJSONSchema(); err == nil && js != nil {
			// Round-trip through JSON to land in a plain map[string]any (our wire type).
			if b, err := json.Marshal(js); err == nil {
				var params map[string]any
				if json.Unmarshal(b, &params) == nil {
					spec.Parameters = params
				}
			}
		}
	}
	if spec.Parameters == nil {
		spec.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return spec
}
