package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/orka-oss/orka_control_layer/llm"
)

func TestTitleResponseRejectsIncompleteAndToolProtocol(t *testing.T) {
	cases := []struct {
		name     string
		response llm.Response
	}{
		{"structured tool call", llm.Response{Content: "Plausible title", FinishReason: "stop", ToolCalls: []llm.ToolCall{{Name: "search", Arguments: `{}`}}}},
		{"truncated", llm.Response{Content: "A truncated title", FinishReason: "length"}},
		{"tool finish without calls", llm.Response{Content: "Not a final title", FinishReason: "tool_calls"}},
		{"legacy function finish", llm.Response{Content: "Not a title", FinishReason: "function_call"}},
		{"blank", llm.Response{Content: " \n\t"}},
		{"provider answered browser task", llm.Response{Content: "I can't actually visit https://hn.algolia.com, but I can help you prepare a reading list.", FinishReason: "stop"}},
		{"Chinese refusal", llm.Response{Content: "我无法直接浏览网页，但可以为你提供建议。", FinishReason: "stop"}},
		{"quotes only", llm.Response{Content: "“”"}},
		{"DSML", llm.Response{Content: "<｜DSML｜tool_calls><｜DSML｜invoke name=\"search\">"}},
		{"ASCII DSML", llm.Response{Content: "<|DSML|tool_calls>"}},
		{"XML calls", llm.Response{Content: "<tool_calls><tool_call>search</tool_call></tool_calls>"}},
		{"XML invoke", llm.Response{Content: "<function_calls><invoke name=\"search\"><parameter name=\"q\">x</parameter></invoke></function_calls>"}},
		{"XML singular", llm.Response{Content: "<tool_call>{\"name\":\"search\"}</tool_call>"}},
		{"XML function", llm.Response{Content: "<function=search>{}</function>"}},
		{"XML uppercase", llm.Response{Content: "<TOOL_CALL>{}</TOOL_CALL>"}},
		{"protocol after title", llm.Response{Content: "A plausible first line\n<tool_call>{}</tool_call>"}},
		{"protocol after truncation boundary", llm.Response{Content: strings.Repeat("正常", 40) + "<｜DSML｜tool_calls>"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snippet := titleSnippet("用户原始问题：分析模拟数据")
			title := snippet
			if replacement := titleFromResponse(tc.response); replacement != "" {
				title = replacement
			}
			if title != snippet {
				t.Fatalf("invalid model title overwrote snippet: %q", title)
			}
		})
	}
}

func TestTitleResponseKeepsMultilingualCleaningAndRuneLimit(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"  “客服工单 SLA 审计”  ", "客服工单 SLA 审计"},
		{"日本語の分析", "日本語の分析"},
		{"تحليل البيانات", "تحليل البيانات"},
		{"Résumé des résultats", "Résumé des résultats"},
		{"Useful title\nExtra explanation", "Useful title"},
		{strings.Repeat("数", 31), strings.Repeat("数", 30) + "…"},
		{strings.Repeat("🧪", 31), strings.Repeat("🧪", 30) + "…"},
		{"XML tools overview", "XML tools overview"},
		{"DSML protocol debugging", "DSML protocol debugging"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			for _, finish := range []string{"", "stop"} {
				got := titleFromResponse(llm.Response{Content: tc.input, FinishReason: finish})
				if got != tc.want || !utf8.ValidString(got) {
					t.Fatalf("title=%q want=%q", got, tc.want)
				}
			}
		})
	}
}
