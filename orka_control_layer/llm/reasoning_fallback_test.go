package llm

import "testing"

// The run that motivated the near-empty case: 1,061 seconds, 29 tool calls,
// eight verified deliverables — and the user saw the single character "D". The
// summary had gone to reasoning_content while one stray character leaked into
// content, so content was not empty and the empty-only check never fired.
func TestReasoningFallbackRecoversANearEmptyAnswer(t *testing.T) {
	long := "四个基准全部通过:埃氏筛 pi(1e6)=78498,五种排序结果一致,Dijkstra 与 Bellman-Ford 最短路一致,背包两法同为 2877。" +
		"性能数据已写入 bench_all.csv,图表见 bench_chart.png,完整分析见 bench_report.md。"
	out := Response{Content: "D", Reasoning: long}
	applyReasoningFallback(&out)
	if out.Content != long {
		t.Fatalf("a one-character reply beside %d chars of reasoning was left as the answer: %q",
			len(long), out.Content)
	}
}

func TestReasoningFallbackRecoversAnEmptyAnswer(t *testing.T) {
	out := Response{Content: "", Reasoning: "the whole answer"}
	applyReasoningFallback(&out)
	if out.Content != "the whole answer" {
		t.Fatalf("content = %q", out.Content)
	}
}

// The load-bearing direction. Short answers are legitimate and asserted by the
// eval suite; replacing one with the model's scratch work would be a regression
// far worse than the bug being fixed.
func TestReasoningFallbackLeavesRealShortAnswers(t *testing.T) {
	long := ""
	for i := 0; i < 60; i++ {
		long += "reasoning about the arithmetic step by step. "
	}
	for _, answer := range []string{"7097663", "PAR3", "完成", "42", "Mozilla", "DONE3", "LATOK"} {
		out := Response{Content: answer, Reasoning: long}
		applyReasoningFallback(&out)
		if out.Content != answer {
			t.Errorf("a legitimate short answer %q was replaced by reasoning", answer)
		}
	}
}

// A turn that is calling a tool has not answered yet; its reasoning is scratch
// work and must never become the assistant's message.
func TestReasoningFallbackIgnoresToolTurns(t *testing.T) {
	out := Response{Content: "", Reasoning: "let me look that up", ToolCalls: []ToolCall{{ID: "1", Name: "web_search"}}}
	applyReasoningFallback(&out)
	if out.Content != "" {
		t.Fatalf("content = %q on a tool-calling turn", out.Content)
	}
}

// Without reasoning there is nothing to fall back to, and a one-character reply
// must be left exactly as the model sent it.
func TestReasoningFallbackWithoutReasoning(t *testing.T) {
	for _, answer := range []string{"", "D"} {
		out := Response{Content: answer}
		applyReasoningFallback(&out)
		if out.Content != answer {
			t.Errorf("content %q changed with no reasoning present", answer)
		}
	}
}

// A short reply beside SHORT reasoning is far more likely to be the real answer
// than a leak, so the substitution needs both conditions.
func TestReasoningFallbackNeedsSubstantialReasoning(t *testing.T) {
	out := Response{Content: "D", Reasoning: "hmm"}
	applyReasoningFallback(&out)
	if out.Content != "D" {
		t.Fatalf("content = %q; a brief thought is not an answer either", out.Content)
	}
}
