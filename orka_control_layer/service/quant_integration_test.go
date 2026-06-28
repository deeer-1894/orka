package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// TestQuantToolChain drives the pipeline's TOOL mechanics end-to-end without the
// LLM: seed → backtest → ingest (the human-review gate target) → list → combine.
// This proves the factor lifecycle independent of any model availability.
func TestQuantToolChain(t *testing.T) {
	base := t.TempDir()
	email := "chain@test.com"
	ctx := agent.WithMeta(context.Background(), messages.Meta{UserEmail: email})
	seedQuantAssets(base, email)

	bt := backtestTool{baseStorage: base}
	ingest := ingestFactorTool{baseStorage: base}
	port := weightPortfolioTool{baseStorage: base}

	// 1) backtest two momentum/quality factors → real-shaped metrics.
	mkFactor := func(name, expr, dir string) map[string]any {
		raw, _ := bt.Invoke(ctx, map[string]any{"expression": expr})
		var btOut struct {
			Source  string        `json:"source"`
			Metrics FactorMetrics `json:"metrics"`
		}
		if err := json.Unmarshal([]byte(raw), &btOut); err != nil {
			t.Fatalf("backtest json: %v (%s)", err, raw)
		}
		if btOut.Source != "python" && btOut.Source != "stub" {
			t.Fatalf("unexpected backtest source %q", btOut.Source)
		}
		return map[string]any{
			"name": name, "rationale": "from research report", "expression": expr,
			"direction": dir, "metrics": btOut.Metrics,
		}
	}
	fa := mkFactor("momentum", "rank(mom_20)", "long")
	fb := mkFactor("quality", "rank(roe)", "long")

	// 2) ingest both (the act ingest_factor performs after human review).
	for _, f := range []map[string]any{fa, fb} {
		out, _ := ingest.Invoke(ctx, map[string]any{"factor": f})
		var res struct {
			Ingested bool   `json:"ingested"`
			FactorID string `json:"factor_id"`
		}
		_ = json.Unmarshal([]byte(out), &res)
		if !res.Ingested || res.FactorID == "" {
			t.Fatalf("ingest failed: %s", out)
		}
	}

	// 3) the library now lists two approved factors.
	got, err := listFactors(base, email, FactorApproved)
	if err != nil || len(got) != 2 {
		t.Fatalf("listFactors: %v len=%d", err, len(got))
	}

	// 4) combine them into a weighted portfolio (ic-weighted) → weights sum to 1.
	pout, _ := port.Invoke(ctx, map[string]any{"method": "ic_weighted"})
	var p struct {
		PortfolioID string `json:"portfolio_id"`
		Weights     []struct {
			Weight float64 `json:"weight"`
		} `json:"weights"`
	}
	if err := json.Unmarshal([]byte(pout), &p); err != nil {
		t.Fatalf("portfolio json: %v (%s)", err, pout)
	}
	if p.PortfolioID == "" || len(p.Weights) != 2 {
		t.Fatalf("portfolio wrong shape: %s", pout)
	}
	var sum float64
	for _, w := range p.Weights {
		sum += w.Weight
	}
	if sum < 0.98 || sum > 1.02 {
		t.Fatalf("portfolio weights sum to %v, want ~1 (%s)", sum, pout)
	}

	// 5) ingest must REJECT a schema-invalid factor (the validate gate).
	badOut, _ := ingest.Invoke(ctx, map[string]any{"factor": map[string]any{"name": "x"}})
	var bad struct {
		Ingested bool `json:"ingested"`
	}
	_ = json.Unmarshal([]byte(badOut), &bad)
	if bad.Ingested {
		t.Fatalf("ingest accepted an invalid factor: %s", badOut)
	}
}
