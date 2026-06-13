package service

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"github.com/orka-oss/orka_core/agent"
)

// einoTool adapts our agent.BaseTool to eino's tool.InvokableTool, so the
// existing tool suite (local + MCP) can be handed to an eino ChatModelAgent
// unchanged. The agent.BaseTool already speaks JSON-schema parameters and a
// map-args Invoke, so the adapter is a thin shim over the two eino methods.
type einoTool struct {
	base agent.BaseTool
}

// EinoTool wraps a BaseTool as an eino InvokableTool.
func EinoTool(base agent.BaseTool) tool.InvokableTool { return &einoTool{base: base} }

var _ tool.InvokableTool = (*einoTool)(nil)

// Info builds the eino ToolInfo from the BaseTool's JSON-schema parameters.
func (t *einoTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: t.base.Name(), Desc: t.base.Description()}
	if raw := t.base.Schema(); len(raw) > 0 {
		// Round-trip the plain map[string]any schema into eino's jsonschema.Schema.
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		var js jsonschema.Schema
		if err := json.Unmarshal(b, &js); err != nil {
			return nil, err
		}
		info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&js)
	}
	return info, nil
}

// InvokableRun parses the JSON arguments and dispatches to the BaseTool.
func (t *einoTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args map[string]any
	if argumentsInJSON != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "", err
		}
	}
	return t.base.Invoke(ctx, args)
}

// EinoTools adapts a slice of BaseTools.
func EinoTools(bases []agent.BaseTool) []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(bases))
	for _, b := range bases {
		out = append(out, EinoTool(b))
	}
	return out
}
