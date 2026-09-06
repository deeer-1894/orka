package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/agent"
)

// The end-to-end check f053952 left open. Its unit tests cover
// clearedToolArgument and the floor arithmetic; nothing drove the REAL eino
// reduction middleware over a write-heavy history, which is where the two
// defects actually showed:
//
//	arguments  53,959 chars   (file_write alone: 46,774)
//	results    16,116 chars   (file_write's own: 630, it returns "ok")
//
// Context climbed 23k -> 45k monotonically and the clear pass did not run once
// in 16 cycles at a 24k threshold, because the payload sat in the ARGUMENT and
// the floor sat above any single clearable item.
//
// Reproducible in CI, unlike the live rerun — a prompt-driven run cannot be
// relied on to reproduce a given context shape.

// writeCall appends one file_write call and its terse result, mirroring the
// measured shape: kilobytes of source in the argument, "ok" coming back.
func writeCall(msgs []*schema.Message, id, path string, sourceBytes int) []*schema.Message {
	var src strings.Builder
	for i := 0; src.Len() < sourceBytes; i++ {
		fmt.Fprintf(&src, "def step_%d(corpus, k1=%d, b=0.%d):\n    scores = rank(corpus, k1, b)\n    return scores[:%d]\n\n",
			i, i%5, i%9, i%20)
	}
	args, _ := json.Marshal(map[string]string{"path": path, "content": src.String()})
	asst := schema.AssistantMessage("", nil)
	asst.ToolCalls = []schema.ToolCall{{
		ID:       id,
		Function: schema.FunctionCall{Name: "file_write", Arguments: string(args)},
	}}
	return append(msgs, asst, &schema.Message{
		Role: schema.Tool, ToolCallID: id, ToolName: "file_write",
		Content: "wrote " + path,
	})
}

func historyTokens(msgs []*schema.Message) int64 {
	n, _, _, _ := probeCount(msgs)
	return n
}

// namedTool is a stand-in whose only job is to carry a name: l0ClearConfigs
// keys its per-tool clear handlers off the run's tool set.
type namedTool struct{ name string }

func (t namedTool) Name() string                                           { return t.name }
func (t namedTool) Description() string                                    { return t.name }
func (t namedTool) Schema() map[string]any                                 { return map[string]any{"type": "object"} }
func (t namedTool) Invoke(context.Context, map[string]any) (string, error) { return "", nil }

// runReduction drives the production middleware chain over a history and
// returns it as the model would next see it.
func runReduction(t *testing.T, msgs []*schema.Message) []*schema.Message {
	t.Helper()
	base := t.TempDir()
	tools := []agent.BaseTool{namedTool{"file_write"}, namedTool{"file_read"}}
	handlers := contextHandlers(context.Background(), base, "u@x.com", "test", tools, nil)
	if len(handlers) == 0 {
		t.Fatal("no context middlewares were constructed")
	}
	state := &adk.ChatModelAgentState{Messages: msgs}
	ctx := context.Background()
	for _, h := range handlers {
		newCtx, newState, err := h.BeforeModelRewriteState(ctx, state, nil)
		if err != nil {
			t.Fatalf("middleware failed: %v", err)
		}
		if newState != nil {
			state = newState
		}
		if newCtx != nil {
			ctx = newCtx
		}
	}
	return state.Messages
}

// The headline case: a history whose bulk is file_write ARGUMENTS must actually
// shrink. Before f053952 this reclaimed exactly zero.
func TestReductionShrinksAWriteHeavyHistory(t *testing.T) {
	msgs := []*schema.Message{schema.UserMessage("build a retrieval engine")}
	for i, name := range []string{"corpus.py", "tokenizer.py", "index.py", "scoring.py", "queries.py", "eval.py"} {
		msgs = writeCall(msgs, string(rune('a'+i)), name, 16000)
	}
	before := historyTokens(msgs)
	if before < clearAboveTokens {
		t.Fatalf("fixture is only %d tokens; it must exceed the %d threshold to exercise the clear pass",
			before, clearAboveTokens)
	}

	after := historyTokens(runReduction(t, msgs))
	if after >= before {
		t.Fatalf("write-heavy history did not shrink: %d -> %d tokens (the payload is in the ARGUMENT, "+
			"which clearing used to pass through untouched)", before, after)
	}
	if reclaimed := before - after; reclaimed < clearFloorTokens {
		t.Fatalf("reclaimed only %d tokens; eino skips the whole pass below %d, so this would not have run",
			reclaimed, clearFloorTokens)
	}
}

// Clearing must leave the model able to get the content back. A cleared
// file_write points at the path it wrote — the file is already there, so no
// archive is needed and the pointer stays correct even if rewritten later.
func TestClearedWriteStillNamesItsPath(t *testing.T) {
	msgs := []*schema.Message{schema.UserMessage("build it")}
	for i, name := range []string{"corpus.py", "tokenizer.py", "index.py", "scoring.py", "queries.py", "eval.py"} {
		msgs = writeCall(msgs, string(rune('a'+i)), name, 16000)
	}
	out := runReduction(t, msgs)

	var cleared, named int
	for _, m := range out {
		if m == nil || m.Role != schema.Assistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.Function.Name != "file_write" {
				continue
			}
			if !strings.Contains(tc.Function.Arguments, "compute_something") {
				cleared++
				if strings.Contains(tc.Function.Arguments, ".py") {
					named++
				}
			}
		}
	}
	if cleared == 0 {
		t.Fatal("no file_write argument was cleared")
	}
	if named != cleared {
		t.Fatalf("%d of %d cleared writes no longer name the file they wrote — the model cannot read it back",
			cleared-named, cleared)
	}
}

// A history below the threshold must be left completely alone: rewriting it
// costs prefix-cache hits (98% -> 63% measured against this provider) for no
// benefit.
func TestReductionLeavesASmallHistoryUntouched(t *testing.T) {
	msgs := []*schema.Message{schema.UserMessage("hi")}
	msgs = writeCall(msgs, "a", "small.py", 200)
	before := historyTokens(msgs)
	if before >= clearAboveTokens {
		t.Fatalf("fixture is %d tokens; it must stay under %d for this case", before, clearAboveTokens)
	}
	if after := historyTokens(runReduction(t, msgs)); after != before {
		t.Fatalf("a %d-token history was rewritten anyway (%d after), invalidating the prefix cache for nothing",
			before, after)
	}
}

// Reports what the pass is worth on the measured shape, so the effect has a
// number rather than a pass/fail. Logged, not asserted: the exact figure moves
// with the fixture and with eino's tokenizer.
func TestReductionWriteHeavyMagnitude(t *testing.T) {
	msgs := []*schema.Message{schema.UserMessage("build a retrieval engine")}
	for i, name := range []string{"corpus.py", "tokenizer.py", "index.py", "scoring.py", "queries.py", "eval.py", "param_sweep.py", "benchmark.py"} {
		msgs = writeCall(msgs, string(rune('a'+i)), name, 16000)
	}
	before := historyTokens(msgs)
	after := historyTokens(runReduction(t, msgs))
	t.Logf("write-heavy history: %d -> %d tokens (reclaimed %d, %.0f%%)",
		before, after, before-after, 100*float64(before-after)/float64(before))
}
