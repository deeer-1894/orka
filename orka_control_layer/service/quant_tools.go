package service

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/pathsafe"
)

// quant_tools.go — the compute tools: backtest a factor expression and evolve
// it with genetic programming. Both prefer the real Python harness in the
// workspace (quant/backtest_runner.py, quant/gp_evolve.py) and fall back to a
// DETERMINISTIC stub so the pipeline runs end-to-end even before the quant core
// is filled in. The result always reports `source` ("python" | "stub") so the
// run record never lies about which path produced the numbers.

const quantExecTimeout = 90 * time.Second

// QuantTools is the factor-pipeline tool set, wired by main with the storage
// base path; the providers append it to every run's tool set (like ArtifactTools).
var QuantTools []agent.BaseTool

// BuildQuantTools constructs the quant pipeline tools bound to the workspace
// storage root. The two pure-logic gates (validate_factor, factor_agreement)
// need no storage; the rest derive the per-user root from the run's identity.
func BuildQuantTools(baseStorage string) []agent.BaseTool {
	return []agent.BaseTool{
		validateFactorTool{},
		factorAgreementTool{},
		backtestTool{baseStorage: baseStorage},
		gpEvolveTool{baseStorage: baseStorage},
		ingestFactorTool{baseStorage: baseStorage},
		weightPortfolioTool{baseStorage: baseStorage},
	}
}

// --- backtest ----------------------------------------------------------------

type backtestTool struct{ baseStorage string }

func (backtestTool) Name() string { return "backtest" }
func (backtestTool) Description() string {
	return "Backtest a quant factor expression on the workspace price data and return its scorecard {ic, ir, sharpe, turnover, max_dd}. Pass `expression` (and optional `horizon`, `universe`). Uses quant/backtest_runner.py when present, otherwise a deterministic placeholder so the pipeline still runs."
}
func (backtestTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expression": map[string]any{"type": "string", "description": "the factor expression to evaluate"},
			"horizon":    map[string]any{"type": "string", "description": "holding horizon, e.g. 1d/5d/1m"},
			"universe":   map[string]any{"type": "string", "description": "stock universe"},
		},
		"required": []string{"expression"},
	}
}

func (t backtestTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	expr := strings.TrimSpace(argStr(args, "expression"))
	if expr == "" {
		return `{"error":"expression required"}`, nil
	}
	email := agent.MetaFrom(ctx).UserEmail
	seedQuantAssets(t.baseStorage, email)
	root := pathsafe.UserRoot(t.baseStorage, email)
	m, source := runBacktest(ctx, root, expr, argStr(args, "horizon"))
	out := map[string]any{
		"expression": expr,
		"source":     source,
		"metrics":    m,
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// runBacktest tries the python harness, falling back to deterministic stub
// metrics. Returns (metrics, source).
func runBacktest(ctx context.Context, root, expr, horizon string) (FactorMetrics, string) {
	script := filepath.Join(root, "quant", "backtest_runner.py")
	if fileExists(script) {
		if m, ok := runPyMetrics(ctx, root, script, "--expression", expr, "--horizon", horizon); ok {
			return m, "python"
		}
	}
	return stubMetrics(expr), "stub"
}

// --- gp_evolve ---------------------------------------------------------------

type gpEvolveTool struct{ baseStorage string }

func (gpEvolveTool) Name() string { return "gp_evolve" }
func (gpEvolveTool) Description() string {
	return "Genetic-programming search that evolves a seed factor expression toward higher backtest fitness. Pass `expression` (the seed). Returns {expression, fitness, metrics, source}. Uses quant/gp_evolve.py when present, otherwise a deterministic refinement so the pipeline still runs."
}
func (gpEvolveTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expression": map[string]any{"type": "string", "description": "the seed factor expression to evolve"},
			"generations": map[string]any{"type": "integer", "description": "GP generations (optional)"},
		},
		"required": []string{"expression"},
	}
}

func (t gpEvolveTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	seed := strings.TrimSpace(argStr(args, "expression"))
	if seed == "" {
		return `{"error":"expression required"}`, nil
	}
	email := agent.MetaFrom(ctx).UserEmail
	seedQuantAssets(t.baseStorage, email)
	root := pathsafe.UserRoot(t.baseStorage, email)
	script := filepath.Join(root, "quant", "gp_evolve.py")
	if fileExists(script) {
		if out, ok := runPyJSON(ctx, root, script, "--seed-expression", seed); ok {
			out["source"] = "python"
			b, _ := json.Marshal(out)
			return string(b), nil
		}
	}
	// Deterministic refinement: normalize the seed and add a stabilizing term,
	// then score it like the backtest would.
	evolved := stubEvolve(seed)
	m := stubMetrics(evolved)
	out := map[string]any{
		"expression": evolved,
		"fitness":    round2(0.6*m.Sharpe + 8*m.IC), // a simple composite fitness
		"metrics":    m,
		"source":     "stub",
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// --- shared helpers ----------------------------------------------------------

func argStr(args map[string]any, k string) string {
	if v, ok := args[k]; ok && v != nil {
		return strings.TrimSpace(toStr(v))
	}
	return ""
}

func toStr(v any) string {
	switch s := v.(type) {
	case string:
		return s
	default:
		b, _ := json.Marshal(v)
		return strings.Trim(string(b), `"`)
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// runPyJSON runs `python3 <script> <args...>` in root and parses the last JSON
// object printed on stdout.
func runPyJSON(ctx context.Context, root, script string, args ...string) (map[string]any, bool) {
	cctx, cancel := context.WithTimeout(ctx, quantExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "python3", append([]string{script}, args...)...)
	cmd.Dir = root
	outBytes, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	line := lastJSONLine(string(outBytes))
	if line == "" {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal([]byte(line), &m) != nil {
		return nil, false
	}
	return m, true
}

// runPyMetrics runs the harness and extracts a FactorMetrics from its JSON
// (accepting either a flat object or {metrics:{…}}).
func runPyMetrics(ctx context.Context, root, script string, args ...string) (FactorMetrics, bool) {
	m, ok := runPyJSON(ctx, root, script, args...)
	if !ok {
		return FactorMetrics{}, false
	}
	src := m
	if inner, ok := m["metrics"].(map[string]any); ok {
		src = inner
	}
	fm := FactorMetrics{
		IC:       num(src["ic"]),
		IR:       num(src["ir"]),
		Sharpe:   num(src["sharpe"]),
		Turnover: num(src["turnover"]),
		MaxDD:    num(src["max_dd"]),
		Periods:  int(num(src["periods"])),
	}
	return fm, true
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}

func lastJSONLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			return s
		}
	}
	return ""
}

// stubMetrics derives reproducible-but-varied metrics from the expression, so
// different factors get different (stable) scorecards without a data feed.
func stubMetrics(expr string) FactorMetrics {
	h := fnv.New64a()
	_, _ = h.Write([]byte(expr))
	seed := h.Sum64()
	// map portions of the hash into plausible ranges
	r := func(shift uint, lo, hi float64) float64 {
		x := float64((seed>>shift)&0xffff) / float64(0xffff)
		return round3(lo + x*(hi-lo))
	}
	ic := r(0, -0.04, 0.12)
	icStd := r(16, 0.04, 0.10)
	ir := 0.0
	if icStd > 0 {
		ir = round3(ic / icStd)
	}
	return FactorMetrics{
		IC:       ic,
		IR:       ir,
		Sharpe:   r(32, -0.4, 2.4),
		Turnover: r(48, 0.05, 0.6),
		MaxDD:    -math.Abs(r(8, 0.05, 0.45)),
		Periods:  250,
	}
}

// stubEvolve produces a deterministic "evolved" expression: wrap the seed in a
// cross-sectional rank and add a short-horizon momentum stabilizer (idempotent
// if already wrapped).
func stubEvolve(seed string) string {
	s := strings.TrimSpace(seed)
	if !strings.HasPrefix(s, "rank(") {
		s = "rank(" + s + ")"
	}
	if !strings.Contains(s, "mom_20") {
		s = s + " + 0.2*rank(mom_20)"
	}
	return s
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }
