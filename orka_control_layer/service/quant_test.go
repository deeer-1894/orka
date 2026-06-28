package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"
)

func TestValidateFactorMap(t *testing.T) {
	ok := map[string]any{"name": "val", "rationale": "low pe outperforms", "expression": "rank(-pe)", "direction": "long"}
	if errs := validateFactorMap(ok); len(errs) != 0 {
		t.Fatalf("valid factor flagged: %v", errs)
	}
	bad := map[string]any{"name": "", "rationale": "", "expression": "pe", "direction": "up"}
	errs := validateFactorMap(bad)
	if len(errs) < 3 {
		t.Fatalf("expected multiple errors, got %v", errs)
	}
}

func TestFactorAgreement(t *testing.T) {
	mk := func(name, rat, expr string) map[string]any {
		return map[string]any{"name": name, "rationale": rat, "expression": expr}
	}
	a := []any{
		mk("momentum", "rising prices keep rising over twenty days", "rank(mom_20)"),
		mk("quality", "high roe firms outperform peers", "rank(roe)"),
	}
	b := []any{
		mk("mom", "rising prices keep rising over twenty days window", "rank(mom_20)"),
		mk("vol", "high volatility stocks lag the market badly", "rank(-vol_20)"),
	}
	out, _ := factorAgreementTool{}.Invoke(context.Background(), map[string]any{"set_a": a, "set_b": b})
	var res struct {
		Agreement float64 `json:"agreement"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("bad json: %v (%s)", err, out)
	}
	// one of two pairs match → agreement = 2*1/(2+2) = 0.5
	if math.Abs(res.Agreement-0.5) > 0.01 {
		t.Fatalf("agreement = %v, want ~0.5 (%s)", res.Agreement, out)
	}
}

func TestComputeWeights(t *testing.T) {
	fs := []Factor{
		{Metrics: FactorMetrics{IC: 0.10, MaxDD: -0.10}},
		{Metrics: FactorMetrics{IC: 0.05, MaxDD: -0.20}},
		{Metrics: FactorMetrics{IC: -0.02, MaxDD: -0.40}},
	}
	for _, method := range []string{"equal", "ic_weighted", "risk_parity"} {
		w := computeWeights(method, fs)
		var sum float64
		for _, x := range w {
			if x < 0 {
				t.Fatalf("%s produced negative weight %v", method, x)
			}
			sum += x
		}
		if math.Abs(sum-1) > 1e-6 {
			t.Fatalf("%s weights sum to %v, want 1", method, sum)
		}
	}
	// ic_weighted: the negative-IC factor should get zero weight.
	w := computeWeights("ic_weighted", fs)
	if w[2] != 0 {
		t.Fatalf("ic_weighted gave the negative-IC factor weight %v, want 0", w[2])
	}
}

func TestFactorStoreRoundTrip(t *testing.T) {
	base := t.TempDir()
	email := "quant@test.com"
	f := Factor{FactorID: "f1", Name: "momentum", Expression: "rank(mom_20)", Direction: "long", Status: FactorApproved, CreatedAt: 1}
	if err := saveFactor(base, email, f); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := listFactors(base, email, FactorApproved)
	if err != nil || len(got) != 1 || got[0].FactorID != "f1" {
		t.Fatalf("list: %v %+v", err, got)
	}
	// status filter excludes it
	if pending, _ := listFactors(base, email, FactorBacktested); len(pending) != 0 {
		t.Fatalf("status filter leaked: %+v", pending)
	}
}

func TestStubMetricsDeterministic(t *testing.T) {
	a := stubMetrics("rank(mom_20)")
	b := stubMetrics("rank(mom_20)")
	if a != b {
		t.Fatalf("stub metrics not deterministic: %+v vs %+v", a, b)
	}
	if stubMetrics("rank(roe)") == a {
		t.Fatalf("different expressions produced identical metrics")
	}
}
