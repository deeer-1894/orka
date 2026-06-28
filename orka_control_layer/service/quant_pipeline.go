package service

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_control_layer/db"
)

// quant_pipeline.go — the production pipeline: a Workflow DAG that turns ONE
// research report into ingested quant factors, plus a batch driver that fans
// the DAG out over every report dropped in the workspace reports/ folder (one
// isolated run each, so a single bad PDF can't sink the daily batch).
//
//	parse → propose_a ┐
//	      → propose_b ┴→ agree(double-blind gate) → evolve(GP+backtest)
//	                     → validate(schema) → review → ingest(library)

const factorAgreementThreshold = "0.7"

// FactorPipelineWorkflow builds the canonical DAG for one report. The report
// path is baked into the parse step (the workflow engine substitutes {{step}}
// from prior OUTPUTS, so the initial input is injected here at build time).
func FactorPipelineWorkflow(owner, reportPath string) db.Workflow {
	return db.Workflow{
		OwnerEmail: owner,
		Name:       "因子流水线 · " + filepath.Base(reportPath),
		// Every step carries a retry policy: a long flow makes many model calls and
		// any one can hit a transient `stream read: unexpected EOF`; without retry a
		// single blip aborts the whole pipeline (default on_error = stop).
		Steps: []db.WorkflowStep{
			{
				Name:    "parse",
				Prompt:  "委托 report_parser 解析研报文件 `" + reportPath + "`,提取其中的自然语言投资逻辑(每条一句话,保留原文措辞)。只输出编号清单,不要额外解释。",
				OnError: "retry:2",
			},
			{
				Name:      "propose_a",
				DependsOn: []string{"parse"},
				Prompt:    "这是第一次独立提取(双盲A)。委托 factor_proposer 把下面的投资逻辑转成已通过 validate_factor 校验的因子,只输出 JSON 数组,不要输出推理过程:\n{{parse}}",
				OnError:   "retry:2",
			},
			{
				Name:      "propose_b",
				DependsOn: []string{"parse"},
				Prompt:    "这是第二次独立提取(双盲B),请独立思考、不要照抄。委托 factor_proposer 把下面的投资逻辑转成已通过 validate_factor 校验的因子,只输出 JSON 数组,不要输出推理过程:\n{{parse}}",
				OnError:   "retry:2",
			},
			{
				Name:      "agree",
				DependsOn: []string{"propose_a", "propose_b"},
				Prompt: "调用 factor_agreement 工具,把 propose_a 的 JSON 数组作为 set_a、propose_b 的作为 set_b 传入(只传 JSON 数组本身,不要把推理文字传进参数)。\nset_a 来自:{{propose_a}}\nset_b 来自:{{propose_b}}\n根据返回的 agreement,保留达标(>=" +
					factorAgreementThreshold + ")的稳定因子,并给每个保留的因子加一个 `agreement_score` 字段(0-1,用本工具返回的整体 agreement 值)。低一致性的在结尾用一行 `LOW_AGREEMENT: <名称>` 标出。最终只输出保留的因子 JSON 数组。",
				OnError: "retry:2",
			},
			{
				Name:      "evolve",
				DependsOn: []string{"agree"},
				Prompt:    "委托 engineer:对 {{agree}} 中的每个因子,先调用 gp_evolve 进化其 expression,再调用 backtest 得到 {ic,ir,sharpe,turnover,max_dd} 指标,把指标写回每个因子对象。输出带 metrics 的因子 JSON 数组。",
				OnError:   "retry:2",
			},
			{
				Name:      "validate",
				DependsOn: []string{"evolve"},
				Prompt:    "对 {{evolve}} 中每个因子调用 validate_factor 确保 schema 合规;不合规的就修正字段。保留每个因子已有的全部字段(rationale / direction / metrics / agreement_score 等),不要丢。输出全部合规的因子 JSON 数组。",
				OnError:   "retry:2",
			},
			{
				Name:      "review",
				DependsOn: []string{"validate"},
				Prompt:    "委托 factor_reviewer,基于 {{validate}} 生成一张人审推荐表(名称/方向/IC/Sharpe/换手/建议)。",
				OnError:   "continue", // a failed review sheet must not block ingestion
			},
			{
				Name:      "ingest",
				DependsOn: []string{"validate"}, // depend on validate, not review, so a flaky review can't strand ingestion
				Prompt:    "对 {{validate}} 中每个被推荐保留的因子调用 ingest_factor 录入因子库(交互模式下这会触发人工确认)。最后用一句话汇总:录入了几个因子、它们的名称。",
				OnError:   "retry:2",
			},
		},
	}
}

// RunFactorPipeline scans the owner's workspace reports/ folder and runs the
// factor pipeline once per report, each in its own conversation. Per-report
// recover() means one crash/bad-PDF doesn't abort the batch. Returns the
// conversation ids started.
// DiscoverReports lists the report files the pipeline would process for owner.
func (s *ChatService) DiscoverReports(owner string) []string {
	return discoverReports(s.Cfg.Storage.BaseStoragePath, owner)
}

// ListFactors returns the owner's factor library (optionally filtered by status).
func (s *ChatService) ListFactors(owner, status string) ([]Factor, error) {
	return listFactors(s.Cfg.Storage.BaseStoragePath, owner, status)
}

// ListPortfolios returns the owner's saved weighted portfolios.
func (s *ChatService) ListPortfolios(owner string) ([]WeightedPortfolio, error) {
	return listPortfolios(s.Cfg.Storage.BaseStoragePath, owner)
}

// pipelineConcurrency bounds how many reports process at once. Each report is a
// long, many-step flow, so we want throughput (10+/day) without hammering the
// model endpoint or the box; a small pool is the right trade-off.
const pipelineConcurrency = 3

func (s *ChatService) RunFactorPipeline(ctx context.Context, owner string) []string {
	seedQuantAssets(s.Cfg.Storage.BaseStoragePath, owner) // ensure the harness is in the workspace
	reports := discoverReports(s.Cfg.Storage.BaseStoragePath, owner)

	// Assign conversation ids up front (stable order), then process reports with
	// bounded concurrency. One bad report can't sink the batch (per-report
	// recover()); the rest keep going.
	convs := make([]string, len(reports))
	for i := range convs {
		convs[i] = messages.NewID()
	}
	sem := make(chan struct{}, pipelineConcurrency)
	var wg sync.WaitGroup
	for i, rp := range reports {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, rp string) {
			defer wg.Done()
			defer func() { <-sem }()
			s.runOneReport(ctx, owner, rp, convs[i])
		}(i, rp)
	}
	wg.Wait()
	return convs
}

// runOneReport runs the pipeline for a single report with panic isolation, into
// the pre-assigned conversation id.
func (s *ChatService) runOneReport(ctx context.Context, owner, reportPath, convID string) {
	defer func() {
		if r := recover(); r != nil && s.Log != nil {
			s.Log.Error("factor pipeline panicked for report", "report", reportPath, "panic", r)
		}
	}()
	wf := FactorPipelineWorkflow(owner, reportPath)
	s.RunWorkflow(ctx, wf, convID)
}

// discoverReports lists candidate report files under <workspace>/reports/
// (pdf/html/htm/md/markdown/txt), newest first.
func discoverReports(baseStorage, owner string) []string {
	dir := filepath.Join(pathsafe.UserRoot(baseStorage, owner), "reports")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type rep struct {
		path string
		mod  int64
	}
	var reps []rep
	for _, e := range ents {
		if e.IsDir() || !isReportFile(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// the pipeline references the report by a workspace-relative path
		reps = append(reps, rep{path: filepath.ToSlash(filepath.Join("reports", e.Name())), mod: info.ModTime().UnixNano()})
	}
	sort.Slice(reps, func(i, j int) bool { return reps[i].mod > reps[j].mod })
	out := make([]string, 0, len(reps))
	for _, r := range reps {
		out = append(out, r.path)
	}
	return out
}

func isReportFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf", ".html", ".htm", ".md", ".markdown", ".txt":
		return true
	}
	return false
}
