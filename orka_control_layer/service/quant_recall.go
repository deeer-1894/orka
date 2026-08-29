package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/orka-oss/orka_core/agent"
)

// quant_recall.go — long-term memory (P3).
//
// The factor library is already a high-quality semantic record of what this
// desk has tried and what actually scored. Until now the extractor never looked
// at it, so every report started from zero: the same idea got re-proposed under
// a new name, and a family of expressions already known to score well was never
// reused. This tool closes that loop — retrieval over the library, ranked by
// similarity to the thesis being worked on.

type recallFactorsTool struct{ baseStorage string }

func (recallFactorsTool) Name() string { return "recall_similar_factors" }
func (recallFactorsTool) Description() string {
	return "Search the existing factor library for factors similar to an investment thesis you are about to turn into a factor. Call it BEFORE proposing, so you can reuse an expression family that already scored well and avoid duplicating a factor the library already holds. Pass `thesis` (the investment logic in words); optional `limit` (default 5)."
}
func (recallFactorsTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"thesis": map[string]any{"type": "string", "description": "the investment logic you are about to formalize"},
			"limit":  map[string]any{"type": "integer", "description": "how many matches to return (default 5)"},
		},
		"required": []string{"thesis"},
	}
}

func (t recallFactorsTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	thesis := strings.TrimSpace(argStr(args, "thesis"))
	if thesis == "" {
		return `{"error":"thesis required"}`, nil
	}
	limit := 5
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	email := agent.MetaFrom(ctx).UserEmail
	all, err := listFactors(t.baseStorage, email, "")
	if err != nil || len(all) == 0 {
		return `{"matches":[],"note":"factor library is empty — propose from the report alone"}`, nil
	}

	want := tokenize(thesis)
	type scored struct {
		f   Factor
		sim float64
	}
	ranked := make([]scored, 0, len(all))
	for _, f := range all {
		sim := jaccard(want, tokenize(f.Name+" "+f.Rationale+" "+f.Expression))
		if sim > 0.08 { // below this it is noise, not a match
			ranked = append(ranked, scored{f: f, sim: sim})
		}
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].sim > ranked[j].sim })
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	matches := make([]map[string]any, 0, len(ranked))
	for _, r := range ranked {
		matches = append(matches, map[string]any{
			"name":       r.f.Name,
			"expression": r.f.Expression,
			"direction":  r.f.Direction,
			"status":     r.f.Status,
			"ic":         r.f.Metrics.IC,
			"sharpe":     r.f.Metrics.Sharpe,
			"similarity": round2(r.sim),
		})
	}
	out := map[string]any{"matches": matches}
	if len(matches) == 0 {
		out["note"] = "no similar factor on file — this thesis is new"
	} else {
		out["guidance"] = "Reuse the expression family of any HIGH-IC match; if a match is near-identical and already approved, say so instead of proposing a duplicate."
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}
