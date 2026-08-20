package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToWireRequestCarriesToolChoice(t *testing.T) {
	wire := toWireRequest(Request{Model: "m", ToolChoice: "required"})
	if wire.ToolChoice != "required" {
		t.Fatalf("tool choice = %q, want required", wire.ToolChoice)
	}
}

func TestToWireRequestCanDisableThinking(t *testing.T) {
	wire := toWireRequest(Request{Model: "m", DisableThinking: true})
	if wire.ChatTemplateKwargs == nil {
		t.Fatal("chat_template_kwargs missing")
	}
	if wire.ChatTemplateKwargs.EnableThinking {
		t.Fatal("enable_thinking = true, want false")
	}

	plain := toWireRequest(Request{Model: "m"})
	if plain.ChatTemplateKwargs != nil {
		t.Fatalf("unexpected chat_template_kwargs: %#v", plain.ChatTemplateKwargs)
	}
}

func TestToWireRequestNormalizesBooleanToolSchemas(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"enabled": map[string]any{"type": "boolean", "default": true},
			"items":   map[string]any{"type": "array", "items": true},
			"plan": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "object", "additionalProperties": true},
					map[string]any{"type": "null"},
				},
			},
		},
	}
	wire := toWireRequest(Request{Model: "m", Tools: []ToolSpec{{Name: "tool", Parameters: params}}})
	properties := wire.Tools[0].Function.Parameters["properties"].(map[string]any)
	enabled := properties["enabled"].(map[string]any)
	if enabled["default"] != true {
		t.Fatalf("boolean data default changed: %#v", enabled["default"])
	}
	items := properties["items"].(map[string]any)
	if got, ok := items["items"].(map[string]any); !ok || len(got) != 0 {
		t.Fatalf("items:true = %#v, want empty schema object", items["items"])
	}
	plan := properties["plan"].(map[string]any)
	first := plan["anyOf"].([]any)[0].(map[string]any)
	if got, ok := first["additionalProperties"].(map[string]any); !ok || len(got) != 0 {
		t.Fatalf("additionalProperties:true = %#v, want empty schema object", first["additionalProperties"])
	}

	originalItems := params["properties"].(map[string]any)["items"].(map[string]any)
	if originalItems["items"] != true {
		t.Fatalf("input schema mutated: %#v", originalItems["items"])
	}
}

func TestChatStreamSurfacesBareJSONError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"invalid tool schema","type":"invalid_request_error"}}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "")
	_, err := client.ChatStream(context.Background(), Request{Model: "m"}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid tool schema") {
		t.Fatalf("ChatStream error = %v, want provider error", err)
	}
}
