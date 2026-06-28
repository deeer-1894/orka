package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/pathsafe"
)

// File-backed factor store. The library lives INSIDE the user's workspace under
// quant/ so it needs no extra infrastructure and the agent can read it with the
// ordinary file tools (and sql_query over the JSONL index). Per-factor JSON
// files are the source of truth; index.jsonl is a regenerated convenience cache
// for listing / ad-hoc querying.
//
// (Production upgrade: swap this for a Mongo `factors` collection without
// changing the tool surface.)

func quantRoot(baseStorage, email string) string {
	return filepath.Join(pathsafe.UserRoot(baseStorage, email), "quant")
}

func factorsDir(baseStorage, email string) string  { return filepath.Join(quantRoot(baseStorage, email), "factors") }
func portfoliosDir(baseStorage, email string) string { return filepath.Join(quantRoot(baseStorage, email), "portfolios") }

// saveFactor persists one factor (create or overwrite by id) and refreshes the
// JSONL index.
func saveFactor(baseStorage, email string, f Factor) error {
	if f.FactorID == "" {
		return fmt.Errorf("factor_id required")
	}
	dir := factorsDir(baseStorage, email)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, f.FactorID+".json"), b, 0o644); err != nil {
		return err
	}
	return rewriteFactorIndex(baseStorage, email)
}

// listFactors returns all factors (optionally filtered by status), newest first.
// Per-factor JSON files are authoritative.
func listFactors(baseStorage, email, status string) ([]Factor, error) {
	dir := factorsDir(baseStorage, email)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // empty library, not an error
		}
		return nil, err
	}
	var out []Factor
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var f Factor
		if json.Unmarshal(b, &f) != nil {
			continue
		}
		if status != "" && f.Status != status {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// rewriteFactorIndex regenerates quant/factors/index.jsonl from the per-factor
// files so the agent (and sql_query) has one compact, queryable table.
func rewriteFactorIndex(baseStorage, email string) error {
	all, err := listFactors(baseStorage, email, "")
	if err != nil {
		return err
	}
	var sb strings.Builder
	for _, f := range all {
		line, _ := json.Marshal(f)
		sb.Write(line)
		sb.WriteByte('\n')
	}
	return os.WriteFile(filepath.Join(factorsDir(baseStorage, email), "index.jsonl"), []byte(sb.String()), 0o644)
}

func savePortfolio(baseStorage, email string, p WeightedPortfolio) error {
	if p.PortfolioID == "" {
		return fmt.Errorf("portfolio_id required")
	}
	dir := portfoliosDir(baseStorage, email)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	return os.WriteFile(filepath.Join(dir, p.PortfolioID+".json"), b, 0o644)
}

// --- ingest_factor -----------------------------------------------------------

type ingestFactorTool struct{ baseStorage string }

func (ingestFactorTool) Name() string { return "ingest_factor" }
func (ingestFactorTool) Description() string {
	return "Persist an APPROVED factor into the factor library (only call after human review). Pass the full `factor` object (with its backtest metrics). Returns {ingested:true, factor_id}."
}
func (ingestFactorTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"factor": map[string]any{"type": "object", "description": "the approved factor object including metrics"},
		},
		"required": []string{"factor"},
	}
}

func (t ingestFactorTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	raw, _ := args["factor"].(map[string]any)
	if raw == nil {
		raw = args
	}
	if errs := validateFactorMap(raw); len(errs) > 0 {
		b, _ := json.Marshal(map[string]any{"ingested": false, "errors": errs})
		return string(b), nil
	}
	email := agent.MetaFrom(ctx).UserEmail
	f := factorFromMap(raw)
	if f.FactorID == "" {
		f.FactorID = messages.NewID()
	}
	f.OwnerEmail = email
	if f.Status == "" {
		f.Status = FactorApproved
	}
	if f.CreatedAt == 0 {
		f.CreatedAt = time.Now().UnixMilli()
	}
	if err := saveFactor(t.baseStorage, email, f); err != nil {
		b, _ := json.Marshal(map[string]any{"ingested": false, "error": err.Error()})
		return string(b), nil
	}
	b, _ := json.Marshal(map[string]any{"ingested": true, "factor_id": f.FactorID, "name": f.Name})
	return string(b), nil
}

// factorFromMap rehydrates a Factor from loosely-typed tool args (round-tripping
// through JSON so nested metrics decode cleanly).
func factorFromMap(m map[string]any) Factor {
	var f Factor
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, &f)
	if f.Direction != "" {
		f.Direction = strings.ToLower(strings.TrimSpace(f.Direction))
	}
	return f
}

// --- weight_portfolio --------------------------------------------------------

type weightPortfolioTool struct{ baseStorage string }

func (weightPortfolioTool) Name() string { return "weight_portfolio" }
func (weightPortfolioTool) Description() string {
	return "Combine ingested factors into a weighted portfolio. `method` is equal | ic_weighted | risk_parity; `factor_ids` optionally restricts to a subset (default: all approved factors). Returns the portfolio with per-factor weights and combined metrics."
}
func (weightPortfolioTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"method":     map[string]any{"type": "string", "enum": []string{"equal", "ic_weighted", "risk_parity"}},
			"factor_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}

func (t weightPortfolioTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	email := agent.MetaFrom(ctx).UserEmail
	method := strings.ToLower(argStr(args, "method"))
	if method == "" {
		method = "equal"
	}
	want := map[string]bool{}
	if ids, ok := args["factor_ids"].([]any); ok {
		for _, v := range ids {
			want[fmt.Sprint(v)] = true
		}
	}
	all, err := listFactors(t.baseStorage, email, FactorApproved)
	if err != nil {
		return `{"error":"cannot read factor library"}`, nil
	}
	var chosen []Factor
	for _, f := range all {
		if len(want) == 0 || want[f.FactorID] {
			chosen = append(chosen, f)
		}
	}
	if len(chosen) == 0 {
		return `{"error":"no approved factors to combine"}`, nil
	}
	weights := computeWeights(method, chosen)
	p := WeightedPortfolio{
		PortfolioID: messages.NewID(),
		OwnerEmail:  email,
		Method:      method,
		Metrics:     blendMetrics(chosen, weights),
		CreatedAt:   time.Now().UnixMilli(),
	}
	rows := make([]map[string]any, 0, len(chosen))
	for i, f := range chosen {
		p.FactorIDs = append(p.FactorIDs, f.FactorID)
		p.Weights = append(p.Weights, round3(weights[i]))
		rows = append(rows, map[string]any{"factor_id": f.FactorID, "name": f.Name, "weight": round3(weights[i])})
	}
	if err := savePortfolio(t.baseStorage, email, p); err != nil {
		b, _ := json.Marshal(map[string]any{"error": err.Error()})
		return string(b), nil
	}
	b, _ := json.Marshal(map[string]any{
		"portfolio_id": p.PortfolioID,
		"method":       method,
		"weights":      rows,
		"metrics":      p.Metrics,
	})
	return string(b), nil
}

// computeWeights returns normalized weights for the chosen factors per method.
func computeWeights(method string, fs []Factor) []float64 {
	n := len(fs)
	w := make([]float64, n)
	switch method {
	case "ic_weighted":
		var sum float64
		for i, f := range fs {
			v := math.Max(f.Metrics.IC, 0)
			w[i] = v
			sum += v
		}
		normalize(w, sum)
	case "risk_parity":
		var sum float64
		for i, f := range fs {
			risk := math.Abs(f.Metrics.MaxDD)
			if risk < 1e-6 {
				risk = 0.2
			}
			w[i] = 1 / risk
			sum += w[i]
		}
		normalize(w, sum)
	default: // equal
		for i := range w {
			w[i] = 1.0 / float64(n)
		}
	}
	return w
}

func normalize(w []float64, sum float64) {
	if sum <= 0 { // degenerate (e.g. all IC<=0) → fall back to equal weight
		for i := range w {
			w[i] = 1.0 / float64(len(w))
		}
		return
	}
	for i := range w {
		w[i] /= sum
	}
}

// blendMetrics is a weighted average of the constituent scorecards (a rough
// proxy; a real combine would re-backtest the blended signal).
func blendMetrics(fs []Factor, w []float64) FactorMetrics {
	var m FactorMetrics
	for i, f := range fs {
		m.IC += w[i] * f.Metrics.IC
		m.IR += w[i] * f.Metrics.IR
		m.Sharpe += w[i] * f.Metrics.Sharpe
		m.Turnover += w[i] * f.Metrics.Turnover
		m.MaxDD += w[i] * f.Metrics.MaxDD
	}
	m.IC, m.IR, m.Sharpe = round3(m.IC), round3(m.IR), round3(m.Sharpe)
	m.Turnover, m.MaxDD = round3(m.Turnover), round3(m.MaxDD)
	if len(fs) > 0 {
		m.Periods = fs[0].Metrics.Periods
	}
	return m
}
