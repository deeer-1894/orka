package service

import (
	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_core/messages"
)

const (
	humanInputAction     = "human_input"
	runtimeContextAction = "runtime_context"
	humanRequestTag      = "orka_human_request"
	runtimeInputTag      = "orka_runtime_input"
)

// humanChat is used only at authenticated chat/history/clarification ingestion.
// A missing marker is legacy/unknown, never evidence that a message is human.
func humanChat(content string, meta messages.Meta) messages.Message {
	m := messages.Chat(messages.RoleUser, content, meta)
	m.Action = humanInputAction
	return m
}

func runtimeUserMessage(content string) *schema.Message {
	m := schema.UserMessage(content)
	m.Extra = map[string]any{runtimeInputTag: true}
	return m
}

func isHumanRequest(m *schema.Message) bool {
	return m != nil && m.Role == schema.User && m.Extra[humanRequestTag] == true
}
func isRuntimeInput(m *schema.Message) bool { return m != nil && m.Extra[runtimeInputTag] == true }
func isUnknownUserInput(m *schema.Message) bool {
	return m != nil && m.Role == schema.User && !isHumanRequest(m) && !isRuntimeInput(m)
}
