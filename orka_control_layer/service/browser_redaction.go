package service

import (
	"encoding/json"
	"maps"

	"github.com/cloudwego/eino/schema"
)

// Keep operational input in the live call/approval checkpoint, but never copy
// fill values or executable expressions into display and historical summaries.
func toolDisplayArgs(name string, args map[string]any) map[string]any {
	if name != "browser" || args == nil {
		return args
	}
	out := maps.Clone(args)
	for _, key := range []string{"text", "value", "expression", "fields"} {
		if _, ok := out[key]; ok {
			out[key] = "[redacted]"
		}
	}
	return out
}
func browserHistoryMessages(input []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, len(input))
	for i, m := range input {
		out[i] = m
		if m == nil {
			continue
		}
		var copyMessage *schema.Message
		for index, call := range m.ToolCalls {
			if call.Function.Name != "browser" {
				continue
			}
			if copyMessage == nil {
				cp := *m
				cp.ToolCalls = append([]schema.ToolCall(nil), m.ToolCalls...)
				copyMessage = &cp
				out[i] = copyMessage
			}
			var args map[string]any
			if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil {
				copyMessage.ToolCalls[index].Function.Arguments = `{"redacted":"invalid browser arguments"}`
				continue
			}
			raw, _ := json.Marshal(toolDisplayArgs("browser", args))
			copyMessage.ToolCalls[index].Function.Arguments = string(raw)
		}
	}
	return out
}
func browserHistoryDelegates(input []delegateRecord) []delegateRecord {
	out := append([]delegateRecord(nil), input...)
	for i := range out {
		out[i].Message = browserHistoryMessages([]*schema.Message{out[i].Message})[0]
	}
	return out
}
