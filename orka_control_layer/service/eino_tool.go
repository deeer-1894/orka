package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"github.com/orka-oss/orka_core/agent"
)

// toolCacheable lists read-only tools whose result for identical args is stable
// for a short window. Memoizing them dedupes the agent's repeated identical
// lookups (common in long research runs) — same data in, same data out, so it
// cannot change accuracy, only avoids re-paying slow network round-trips.
var toolCacheable = map[string]bool{"web_search": true, "fetch_url": true}

const toolCacheTTL = 5 * time.Minute

type toolCacheEntry struct {
	out string
	at  time.Time
}

var (
	toolCacheMu sync.Mutex
	toolCache   = map[string]toolCacheEntry{}
)

func toolCacheGet(key string) (string, bool) {
	toolCacheMu.Lock()
	defer toolCacheMu.Unlock()
	e, ok := toolCache[key]
	if !ok || time.Since(e.at) > toolCacheTTL {
		return "", false
	}
	return e.out, true
}

func toolCachePut(key, out string) {
	toolCacheMu.Lock()
	defer toolCacheMu.Unlock()
	toolCache[key] = toolCacheEntry{out: out, at: time.Now()}
}

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
//
// A tool failure (a network blip, an upstream 5xx, a search endpoint being
// blocked) must NOT abort the whole agent run — eino's ToolNode treats a
// returned error as fatal (NodeRunError), which would throw away every prior
// step of a long multi-step task. Instead we hand the failure back to the model
// as a tool observation, so it can retry with a different query, switch tools,
// or synthesize from what it already has. Context cancellation is the one
// genuinely fatal case (the run was stopped/killed), so it still propagates.
func (t *einoTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args map[string]any
	if argumentsInJSON != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "tool call failed: invalid arguments JSON: " + err.Error(), nil
		}
	}
	// Short-TTL memoization for read-only tools: skip a redundant slow round-trip
	// when the agent repeats an identical query.
	name := t.base.Name()
	cacheable := toolCacheable[name]
	cacheKey := name + "\x00" + argumentsInJSON
	if cacheable {
		if cached, ok := toolCacheGet(cacheKey); ok {
			return cached, nil
		}
	}
	out, err := t.base.Invoke(ctx, args)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "tool error (recoverable — try a different approach, another tool, or proceed without this result): " + err.Error(), nil
	}
	if captureSalesBIAssist(ctx, name, args, out) {
		out = salesBIAuditResult(name, out)
	}
	if cacheable && out != "" {
		toolCachePut(cacheKey, out)
	}
	return out, nil
}

// EinoTools adapts a slice of BaseTools.
func EinoTools(bases []agent.BaseTool) []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(bases))
	for _, b := range bases {
		out = append(out, EinoTool(b))
	}
	return out
}
