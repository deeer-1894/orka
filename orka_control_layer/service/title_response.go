package service

import (
	"strings"

	"github.com/orka-oss/orka_control_layer/llm"
)

// titleFromResponse returns no replacement for incomplete or protocol output,
// leaving the conversation's existing snippet intact. Inspect the full content
// before cleanTitle discards later lines or truncates it to a short label.
func titleFromResponse(resp llm.Response) string {
	if len(resp.ToolCalls) > 0 {
		return ""
	}
	switch resp.FinishReason {
	case "", "stop": // Some compatible services omit finish_reason.
	default:
		return ""
	}
	content := strings.ToLower(resp.Content)
	firstLine := strings.TrimSpace(strings.SplitN(content, "\n", 2)[0])
	// A title-only call has no browsing tools. If a provider answers the task
	// instead of labelling it, keep the user's existing snippet.
	for _, prefix := range []string{"i can't", "i cannot", "i’m unable", "i'm unable", "as an ai", "i don't have", "我无法", "我不能", "我目前无法", "抱歉", "很抱歉"} {
		if strings.HasPrefix(firstLine, prefix) {
			return ""
		}
	}
	if len(strings.Fields(firstLine)) > 8 {
		return ""
	}
	if strings.Contains(content, "<｜dsml｜") || strings.Contains(content, "<|dsml|") {
		return ""
	}
	// Recognize a small set of actual tool-protocol tag names. Ordinary titles
	// discussing XML or DSML remain valid; no general HTML parser is needed.
	for rest := content; ; {
		at := strings.IndexByte(rest, '<')
		if at < 0 {
			break
		}
		rest = rest[at+1:]
		tag := strings.TrimPrefix(rest, "/")
		if end := strings.IndexAny(tag, " \t\r\n/>="); end >= 0 {
			tag = tag[:end]
		}
		switch tag {
		case "tool_call", "tool_calls", "function_call", "function_calls", "invoke", "function", "parameter":
			return ""
		}
	}
	return cleanTitle(resp.Content)
}
