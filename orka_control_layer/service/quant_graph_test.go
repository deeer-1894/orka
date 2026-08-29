package service

import (
	"context"
	"strings"
	"testing"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
)

func graphSvc(t *testing.T) (*ChatService, string) {
	t.Helper()
	base := t.TempDir()
	return &ChatService{Cfg: &config.Config{}}, base
}

// TestDeterministicStagesNeedNoModel is the point of P2: agreement scoring,
// GP+backtest, schema validation and persistence are mechanical. They used to be
// four separate full-orchestrator LLM steps; here they run in-process with no
// model configured at all.
func TestDeterministicStagesNeedNoModel(t *testing.T) {
	t.Setenv("ORKA_BACKTEST_OFFLINE", "1")
	s, base := graphSvc(t)
	s.Cfg.Storage.BaseStoragePath = base
	owner := "graph@test.com"
	ctx := agent.WithMeta(context.Background(), messages.Meta{UserEmail: owner})

	mk := func(name, expr, rationale string) Factor {
		return Factor{Name: name, Expression: expr, Rationale: rationale, Direction: "long"}
	}
	st := &PipelineState{
		ReportPath: "reports/x.md",
		SetA: []Factor{
			mk("momentum", "rank(mom_20)", "rising prices keep rising over twenty days"),
			mk("quality", "rank(roe)", "high roe firms outperform peers"),
		},
		SetB: []Factor{
			mk("mom", "rank(mom_20)", "rising prices keep rising over twenty days window"),
			mk("vol", "rank(-vol_20)", "high volatility stocks lag the market badly"),
		},
	}

	// agree: 1 of 2 pairs match → 2*1/(2+2) = 0.5, and the matched factor is kept.
	if err := s.stageAgree(ctx, st); err != nil {
		t.Fatalf("agree: %v", err)
	}
	if st.Agreement < 0.49 || st.Agreement > 0.51 {
		t.Fatalf("agreement = %v, want ~0.5", st.Agreement)
	}
	if len(st.Kept) != 1 || st.Kept[0].Name != "momentum" {
		t.Fatalf("kept = %+v, want just the matched factor", st.Kept)
	}
	if st.Kept[0].AgreementScore == 0 {
		t.Fatal("kept factor should carry the agreement score")
	}

	// evolve: expression refined + metrics filled, all without a model.
	before := st.Kept[0].Expression
	if err := s.stageEvolve(ctx, st); err != nil {
		t.Fatalf("evolve: %v", err)
	}
	if st.Kept[0].Metrics.Periods == 0 {
		t.Fatal("evolve should have populated backtest metrics")
	}
	if st.Kept[0].Expression == before {
		t.Logf("note: expression unchanged (%q) — acceptable if GP found no improvement", before)
	}

	// validate → ingest: lands as PENDING review, in the owner's library.
	if err := s.stageValidate(ctx, st); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := s.stageIngest(ctx, st); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	got, err := listFactors(base, owner, FactorBacktested)
	if err != nil || len(got) != 1 {
		t.Fatalf("listFactors pending: %v len=%d", err, len(got))
	}
	if got[0].SourceReportID != "reports/x.md" {
		t.Fatalf("ingested factor lost its source report: %+v", got[0])
	}
}

// TestAgreeGateRejectsDisjointSets: when the two blind extractions share nothing,
// the gate must stop the pipeline rather than pass junk downstream.
func TestAgreeGateRejectsDisjointSets(t *testing.T) {
	s, base := graphSvc(t)
	s.Cfg.Storage.BaseStoragePath = base
	st := &PipelineState{
		SetA: []Factor{{Name: "alpha", Expression: "rank(pe)", Rationale: "cheap stocks win"}},
		SetB: []Factor{{Name: "beta", Expression: "rank(vol_20)", Rationale: "turnover dominates flows"}},
	}
	if err := s.stageAgree(context.Background(), st); err == nil {
		t.Fatal("expected the gate to reject two disjoint extractions")
	}
}

// TestFactorsFromTextIgnoresReasoning is the P2 fix for the observed bug where a
// model's reasoning ended up inside tool arguments: parsing is now typed and
// tolerant, and schema-invalid proposals are dropped instead of flowing on.
func TestFactorsFromTextIgnoresReasoning(t *testing.T) {
	out := `<analysis> I should think about this... </analysis>
Here are my factors:
` + "```json" + `
[{"name":"mom","rationale":"prices trend","expression":"rank(mom_20)","direction":"long"},
 {"name":"broken","rationale":"","expression":"","direction":"sideways"}]
` + "```"
	got := factorsFromText(out)
	if len(got) != 1 {
		t.Fatalf("expected only the schema-valid factor, got %d: %+v", len(got), got)
	}
	if got[0].Name != "mom" {
		t.Fatalf("wrong factor kept: %+v", got[0])
	}
}

func TestParseNumberedList(t *testing.T) {
	in := "研报要点:\n1. 低估值股票在风格切换时修复\n2. 高 ROE 具有持续超额\n3) 高波动长期跑输\n"
	got := parseNumberedList(in)
	if len(got) != 3 {
		t.Fatalf("got %d theses, want 3: %#v", len(got), got)
	}
	if !strings.Contains(got[0], "低估值") {
		t.Fatalf("first thesis wrong: %q", got[0])
	}
}
