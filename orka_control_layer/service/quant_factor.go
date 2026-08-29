package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// quant_factor.go — the two pure-logic gate tools of the pipeline:
//   - validate_factor: schema-checks a proposed factor so the proposer can self
//     correct before anything expensive runs (the lever from 70% → 90%+).
//   - factor_agreement: scores how consistent two independent (double-blind)
//     extractions of the same report are, so only stable factors proceed.

// --- validate_factor ---------------------------------------------------------

type validateFactorTool struct{}

func (validateFactorTool) Name() string { return "validate_factor" }
func (validateFactorTool) Description() string {
	return "Validate a proposed quant factor against the required schema BEFORE backtesting. Pass the factor object under `factor`. Returns {valid:true} or {valid:false, errors:[…]}; if invalid, fix the listed problems and call again until it is valid."
}
func (validateFactorTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"factor": map[string]any{
				"type":        "object",
				"description": "the factor: {name, rationale, expression, direction(long|short|long_short), universe?, horizon?}",
			},
		},
		"required": []string{"factor"},
	}
}

func (validateFactorTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	f, _ := args["factor"].(map[string]any)
	if f == nil {
		// tolerate the fields being passed flat
		f = args
	}
	errs := validateFactorMap(f)
	out := map[string]any{"valid": len(errs) == 0}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

var directionSet = map[string]bool{"long": true, "short": true, "long_short": true}

// KnownSignals is the CONTROLLED VOCABULARY the backtest harness can actually
// evaluate. Proposals used to invent field names (pe_ttm, vol_20d, std_20d);
// the harness could not map them, silently fell back to a random signal, and
// unrelated theses collapsed onto the same factor. Rejecting unknown fields at
// the schema gate turns that silent corruption into a fixable error message.
var KnownSignals = []string{"mom_20", "roe", "value", "vol_20"}

// knownOps are the operators/wrappers the expression grammar allows.
var knownOps = map[string]bool{"rank": true, "zscore": true}

// identRe pulls bare identifiers out of an expression (function names included).
var identRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// unknownSignals returns the identifiers in expr that are neither a known signal
// nor a known operator — i.e. the fields the backtest cannot evaluate.
func unknownSignals(expr string) []string {
	known := map[string]bool{}
	for _, s := range KnownSignals {
		known[s] = true
	}
	var bad []string
	seen := map[string]bool{}
	for _, id := range identRe.FindAllString(expr, -1) {
		low := strings.ToLower(id)
		if known[low] || knownOps[low] || seen[low] {
			continue
		}
		seen[low] = true
		bad = append(bad, id)
	}
	return bad
}

// validateFactorMap returns a list of human-readable schema problems ([] = ok).
func validateFactorMap(f map[string]any) []string {
	var errs []string
	str := func(k string) string {
		if v, ok := f[k]; ok && v != nil {
			return strings.TrimSpace(fmt.Sprint(v))
		}
		return ""
	}
	if str("name") == "" {
		errs = append(errs, "name: required (short factor title)")
	}
	if str("rationale") == "" {
		errs = append(errs, "rationale: required (the natural-language investment logic from the report)")
	}
	expr := str("expression")
	if expr == "" {
		errs = append(errs, "expression: required (a machine-evaluable factor expression, not prose)")
	} else if len(strings.Fields(expr)) == 1 && !exprLooksReal(expr) {
		errs = append(errs, "expression: looks like a bare word, not a factor formula")
	} else if bad := unknownSignals(expr); len(bad) > 0 {
		errs = append(errs, fmt.Sprintf("expression: unknown field(s) %s — the backtest only supports %s (wrap in rank()/zscore(); use a leading minus for \"lower is better\", e.g. rank(-value))",
			strings.Join(bad, ", "), strings.Join(KnownSignals, ", ")))
	}
	dir := strings.ToLower(str("direction"))
	if dir == "" {
		errs = append(errs, "direction: required (one of long|short|long_short)")
	} else if !directionSet[dir] {
		errs = append(errs, "direction: must be one of long|short|long_short, got "+dir)
	}
	return errs
}

// exprLooksReal accepts a single token only if it carries an operator/paren,
// i.e. it is plausibly a formula rather than a noun.
func exprLooksReal(expr string) bool {
	return strings.ContainsAny(expr, "()+-*/<>=.,")
}

// --- factor_agreement --------------------------------------------------------

type factorAgreementTool struct{}

func (factorAgreementTool) Name() string { return "factor_agreement" }
func (factorAgreementTool) Description() string {
	return "Score double-blind consistency between two independent factor extractions of the SAME report. Pass `set_a` and `set_b` (each a list of factor objects). Returns {agreement: 0..1, matched, only_a, only_b}; feed the high-agreement factors forward and flag the rest for human review."
}
func (factorAgreementTool) Schema() map[string]any {
	item := map[string]any{"type": "object"}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"set_a": map[string]any{"type": "array", "items": item, "description": "factors from extraction A"},
			"set_b": map[string]any{"type": "array", "items": item, "description": "factors from extraction B"},
		},
		"required": []string{"set_a", "set_b"},
	}
}

func (factorAgreementTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	a := factorTexts(args["set_a"])
	b := factorTexts(args["set_b"])
	if len(a) == 0 && len(b) == 0 {
		return `{"agreement":0,"matched":[],"only_a":[],"only_b":[],"note":"both sets empty"}`, nil
	}
	matched, onlyA, onlyB := matchFactors(a, b, 0.45)
	denom := len(a) + len(b)
	agreement := 0.0
	if denom > 0 {
		agreement = float64(2*len(matched)) / float64(denom)
	}
	out := map[string]any{
		"agreement": round2(agreement),
		"matched":   matchedNames(matched),
		"only_a":    onlyA.names(),
		"only_b":    onlyB.names(),
	}
	bts, _ := json.Marshal(out)
	return string(bts), nil
}

// factorRef is a comparable view of a factor: a display name + a bag of tokens
// drawn from its name + rationale + expression.
type factorRef struct {
	name   string
	tokens map[string]bool
}

type factorRefs []factorRef

func (fr factorRefs) names() []string {
	out := make([]string, 0, len(fr))
	for _, f := range fr {
		out = append(out, f.name)
	}
	return out
}

func factorTexts(raw any) factorRefs {
	var out factorRefs
	for _, it := range coerceFactorList(raw) {
		// items are usually objects, but tolerate a bare string (a factor name).
		if s, ok := it.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, factorRef{name: trunc(s, 60), tokens: tokenize(s)})
			}
			continue
		}
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		get := func(k string) string {
			if v, ok := m[k]; ok && v != nil {
				return fmt.Sprint(v)
			}
			return ""
		}
		name := strings.TrimSpace(get("name"))
		if name == "" {
			name = strings.TrimSpace(get("expression"))
		}
		out = append(out, factorRef{name: name, tokens: tokenize(get("name") + " " + get("rationale") + " " + get("expression"))})
	}
	return out
}

var jsonArrayRe = regexp.MustCompile(`(?s)\[.*\]`)

// coerceFactorList normalizes the loosely-typed agreement input. The model often
// passes a JSON string (sometimes wrapped in reasoning/markdown) instead of a
// real array; we recover the embedded JSON array rather than failing the gate.
func coerceFactorList(raw any) []any {
	switch v := raw.(type) {
	case []any:
		return v
	case string:
		// pull the outermost [...] out of whatever prose/markdown surrounds it
		if m := jsonArrayRe.FindString(v); m != "" {
			var arr []any
			if json.Unmarshal([]byte(m), &arr) == nil {
				return arr
			}
		}
	case map[string]any:
		// a single factor object → treat as a one-element list
		return []any{v}
	}
	return nil
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}_]+`)

func tokenize(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range wordRe.FindAllString(strings.ToLower(s), -1) {
		if len(w) >= 2 {
			set[w] = true
		}
	}
	return set
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

type matchPair struct {
	a, b string
	sim  float64
}

// matchFactors greedily pairs factors across the two sets by token similarity;
// pairs at/above threshold count as agreement.
func matchFactors(a, b factorRefs, threshold float64) (matched []matchPair, onlyA, onlyB factorRefs) {
	usedB := make([]bool, len(b))
	matchedA := make([]bool, len(a))
	for i := range a {
		best, bestJ := 0.0, -1
		for j := range b {
			if usedB[j] {
				continue
			}
			if s := jaccard(a[i].tokens, b[j].tokens); s > best {
				best, bestJ = s, j
			}
		}
		if bestJ >= 0 && best >= threshold {
			usedB[bestJ] = true
			matchedA[i] = true
			matched = append(matched, matchPair{a: a[i].name, b: b[bestJ].name, sim: round2(best)})
		}
	}
	for i := range a {
		if !matchedA[i] {
			onlyA = append(onlyA, a[i])
		}
	}
	for j := range b {
		if !usedB[j] {
			onlyB = append(onlyB, b[j])
		}
	}
	return matched, onlyA, onlyB
}

func matchedNames(m []matchPair) []map[string]any {
	out := make([]map[string]any, 0, len(m))
	for _, p := range m {
		out = append(out, map[string]any{"a": p.a, "b": p.b, "sim": p.sim})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["sim"].(float64) > out[j]["sim"].(float64) })
	return out
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
