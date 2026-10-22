package main

import (
	"fmt"
	"sort"
)

// runRepeated runs t n times. One run cannot judge cost: the same long task
// measured 296k to 1.7M tokens across runs of an unchanged build. The result
// passes only if every run passed and reports median cost with its range.
func (c *client) runRepeated(t task, n int) result {
	if t.MaxRepeat > 0 && n > t.MaxRepeat {
		n = t.MaxRepeat
	}
	if n <= 1 {
		return c.runTask(t)
	}
	runs := make([]result, 0, n)
	for range n {
		runs = append(runs, c.runTask(t))
	}
	return aggregate(t.ID, runs)
}

func aggregate(id string, runs []result) result {
	out := result{ID: id, Runs: len(runs)}
	tools := make([]int, 0, len(runs))
	tokens := make([]int, 0, len(runs))
	seconds := make([]float64, 0, len(runs))
	for i, r := range runs {
		if r.Pass {
			out.Passes++
		}
		for _, why := range r.Reasons {
			out.Reasons = append(out.Reasons, fmt.Sprintf("run %d: %s", i+1, why))
		}
		out.Status = r.Status
		tools, tokens, seconds = append(tools, r.Tools), append(tokens, r.Tokens), append(seconds, r.Seconds)
	}
	out.Pass = out.Passes == len(runs)
	sort.Ints(tools)
	sort.Ints(tokens)
	sort.Float64s(seconds)
	out.Tools, out.Tokens, out.Seconds = tools[len(tools)/2], tokens[len(tokens)/2], seconds[len(seconds)/2]
	out.TokenRange = &[2]int{tokens[0], tokens[len(tokens)-1]}
	return out
}

func repeatSummary(r result) string {
	if r.Runs <= 1 {
		return ""
	}
	return fmt.Sprintf("  (%d/%d passed, median of %d, tokens %d–%d)", r.Passes, r.Runs, r.Runs, r.TokenRange[0], r.TokenRange[1])
}
