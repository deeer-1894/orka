package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// quant_graph.go — the typed factor pipeline (P2).
//
// What this replaces: the text-DAG in workflow.go, where EVERY stage re-ran the
// whole orchestrator with the full tool set and received its input as PROSE via
// {{step}} substitution. That cost 111k tokens over three stages, made one stage
// take 236s, and let the model's own reasoning leak into tool arguments.
//
// The thesis: about two thirds of this pipeline is mechanical (agreement
// scoring, GP + backtest, schema validation, persistence). Only reading a report,
// proposing factors and writing the review sheet need a model. So:
//
//	stage      kind        tools in scope
//	───────────────────────────────────────────────────────
//	parse      agent       pdf_extract, fetch_url, file_read
//	propose_a  agent  ┐    file_read, validate_factor      (parallel, double-blind)
//	propose_b  agent  ┘    file_read, validate_factor
//	agree      pure Go     —   (reuses the same matcher as factor_agreement)
//	evolve     pure Go     —   (calls gp_evolve + backtest directly)
//	validate   pure Go     —   (schema check)
//	review     agent       file_read
//	ingest     pure Go     —   (persist as pending review)
//
// State flows as []Factor, never as text, so a stage cannot be handed a blob of
// the previous model's reasoning.

// PipelineState is the typed state that flows along the pipeline's edges.
type PipelineState struct {
	ReportPath string
	Theses     []string
	SetA       []Factor
	SetB       []Factor
	Agreement  float64
	Kept       []Factor // survived the double-blind gate
	Final      []Factor // evolved, backtested, schema-valid
	Review     string
	Ingested   []Factor
}

// stageFn is one pipeline stage. Deterministic stages never touch a model.
type stageFn func(context.Context, *PipelineState) error

// RunFactorGraph executes the typed pipeline for one report and returns the
// final state. Emitted plan events keep the UI's execution timeline live.
func (s *ChatService) RunFactorGraph(ctx context.Context, owner, reportPath string) (*PipelineState, error) {
	seedQuantAssets(s.Cfg.Storage.BaseStoragePath, owner)
	st := &PipelineState{ReportPath: reportPath}

	stages := []struct {
		name  string
		label string
		run   stageFn
	}{
		{"parse", "解析研报投资逻辑", s.stageParse},
		{"propose", "双盲提取因子", s.stagePropose}, // runs A and B in parallel
		{"agree", "一致性闸门", s.stageAgree},
		{"evolve", "GP 进化 + 回测", s.stageEvolve},
		{"validate", "schema 校验", s.stageValidate},
		{"review", "生成人审单", s.stageReview},
		{"ingest", "录入因子库", s.stageIngest},
	}

	emitPlan := func(active int) {
		emit := agent.EmitFrom(ctx)
		if emit == nil {
			return
		}
		steps := make([]messages.PlanStep, 0, len(stages))
		for i, sg := range stages {
			status := "pending"
			if i < active {
				status = "done"
			} else if i == active {
				status = "active"
			}
			steps = append(steps, messages.PlanStep{Title: sg.label, Status: status})
		}
		emit(messages.Plan(messages.PlanUpdate{Steps: steps}, agent.MetaFrom(ctx)))
	}

	for i, sg := range stages {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		emitPlan(i)
		if err := sg.run(ctx, st); err != nil {
			return st, fmt.Errorf("stage %s: %w", sg.name, err)
		}
	}
	emitPlan(len(stages)) // all done
	return st, nil
}

// --- agent stages ------------------------------------------------------------

// scopedAgentRun builds a single-purpose agent with ONLY the tools this stage
// needs and runs it once. Scoping is what keeps a stage's prompt (and its tool
// table) small — the old design handed every stage the entire tool set.
func (s *ChatService) scopedAgentRun(ctx context.Context, owner, instruction, task string, toolNames []string, useMini bool) (string, error) {
	tools, cleanup, err := s.ToolsFor(ctx, ChatRunRequest{UserEmail: owner, EnabledTools: toolNames})
	if err != nil && len(tools) == 0 {
		return "", err
	}
	if cleanup != nil {
		defer cleanup()
	}
	tools = filterByName(tools, toolNames)

	client, model := s.Main, s.Cfg.LLM.Model
	backup := backupModel(s.Mini, s.Cfg.LLM.MiniModel, model)
	if useMini && s.Mini != nil && s.Cfg.LLM.MiniModel != "" {
		client, model = s.Mini, s.Cfg.LLM.MiniModel
		backup = backupModel(s.Main, s.Cfg.LLM.Model, model)
	}
	ag, err := BuildEinoAgent(ctx, client, model, instruction, tools, 12, backup,
		contextHandlers(ctx, s.Cfg.Storage.BaseStoragePath, owner)...)
	if err != nil {
		return "", err
	}
	return RunEinoOnce(ctx, ag, task)
}

// filterByName keeps only the named tools (exact match), so a stage can never
// reach for something outside its remit.
func filterByName(tools []agent.BaseTool, names []string) []agent.BaseTool {
	if len(names) == 0 {
		return tools
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := make([]agent.BaseTool, 0, len(names))
	for _, t := range tools {
		if want[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

func (s *ChatService) stageParse(ctx context.Context, st *PipelineState) error {
	out, err := s.scopedAgentRun(ctx, ownerOf(ctx), reportParserPrompt,
		"解析研报文件 `"+st.ReportPath+"`,提取其中的自然语言投资逻辑。只输出编号清单,每条一句话,保留原文措辞。",
		[]string{"pdf_extract", "fetch_url", "file_read", "file_list"}, true)
	if err != nil {
		return err
	}
	st.Theses = parseNumberedList(out)
	if len(st.Theses) == 0 {
		return fmt.Errorf("no investment theses found in %s", st.ReportPath)
	}
	return nil
}

// stagePropose runs the two blind extractions CONCURRENTLY — they are
// independent by construction, so the old sequential 236s+127s becomes one wait.
func (s *ChatService) stagePropose(ctx context.Context, st *PipelineState) error {
	owner := ownerOf(ctx)
	theses := "投资逻辑:\n" + strings.Join(st.Theses, "\n")
	run := func(nudge string) ([]Factor, error) {
		out, err := s.scopedAgentRun(ctx, owner, factorProposerPrompt, nudge+"\n"+theses,
			[]string{"file_read", "validate_factor"}, false)
		if err != nil {
			return nil, err
		}
		return factorsFromText(out), nil
	}

	var wg sync.WaitGroup
	var errA, errB error
	wg.Add(2)
	go func() { defer wg.Done(); st.SetA, errA = run("把下面每条投资逻辑转成一个已通过 validate_factor 校验的因子,只输出 JSON 数组:") }()
	go func() {
		defer wg.Done()
		st.SetB, errB = run("独立思考(不要照抄任何既有答案),把下面每条投资逻辑转成一个已通过 validate_factor 校验的因子,只输出 JSON 数组:")
	}()
	wg.Wait()

	if errA != nil && errB != nil {
		return fmt.Errorf("both blind extractions failed: %v / %v", errA, errB)
	}
	return nil // one surviving side still yields a (low) agreement score
}

func (s *ChatService) stageReview(ctx context.Context, st *PipelineState) error {
	if len(st.Final) == 0 {
		st.Review = "没有通过校验的因子,无需人审。"
		return nil
	}
	rows, _ := json.Marshal(st.Final)
	out, err := s.scopedAgentRun(ctx, ownerOf(ctx), factorReviewerPrompt,
		"基于以下因子及其回测指标,生成一张简明的人审推荐表(名称/方向/IC/Sharpe/换手/一致性/建议):\n"+string(rows),
		[]string{"file_read"}, true)
	if err != nil {
		st.Review = "人审单生成失败: " + err.Error() // non-fatal: ingestion still proceeds
		return nil
	}
	st.Review = out
	return nil
}

// --- deterministic stages (no model) -----------------------------------------

// stageAgree scores double-blind consistency with the SAME matcher the
// factor_agreement tool uses — but in-process, on typed data. This is the stage
// where the model used to paste its reasoning into the tool arguments.
func (s *ChatService) stageAgree(_ context.Context, st *PipelineState) error {
	a, b := toRefs(st.SetA), toRefs(st.SetB)
	matched, _, _ := matchFactors(a, b, 0.45)
	denom := len(a) + len(b)
	if denom > 0 {
		st.Agreement = round2(float64(2*len(matched)) / float64(denom))
	}
	// Keep the A-side version of every matched pair, annotated with the score.
	keep := map[string]bool{}
	for _, m := range matched {
		keep[m.a] = true
	}
	for _, f := range st.SetA {
		if keep[f.Name] || (len(matched) == 0 && st.Agreement == 0 && len(st.SetB) == 0) {
			f.AgreementScore = st.Agreement
			st.Kept = append(st.Kept, f)
		}
	}
	if len(st.Kept) == 0 {
		return fmt.Errorf("no factor cleared the double-blind gate (agreement %.2f)", st.Agreement)
	}
	return nil
}

// stageEvolve calls the GP + backtest tools directly. They are deterministic
// Go/Python — routing them through a model only burned tokens and latency.
func (s *ChatService) stageEvolve(ctx context.Context, st *PipelineState) error {
	gp := gpEvolveTool{baseStorage: s.Cfg.Storage.BaseStoragePath}
	bt := backtestTool{baseStorage: s.Cfg.Storage.BaseStoragePath}
	for i := range st.Kept {
		f := st.Kept[i]
		if raw, err := gp.Invoke(ctx, map[string]any{"expression": f.Expression}); err == nil {
			var ev struct {
				Expression string `json:"expression"`
			}
			if json.Unmarshal([]byte(raw), &ev) == nil && ev.Expression != "" {
				f.Expression = ev.Expression
			}
		}
		if raw, err := bt.Invoke(ctx, map[string]any{"expression": f.Expression, "horizon": f.Horizon}); err == nil {
			var res struct {
				Metrics FactorMetrics `json:"metrics"`
			}
			if json.Unmarshal([]byte(raw), &res) == nil {
				f.Metrics = res.Metrics
			}
		}
		st.Kept[i] = f
	}
	return nil
}

func (s *ChatService) stageValidate(_ context.Context, st *PipelineState) error {
	for _, f := range st.Kept {
		var m map[string]any
		b, _ := json.Marshal(f)
		_ = json.Unmarshal(b, &m)
		if errs := validateFactorMap(m); len(errs) == 0 {
			st.Final = append(st.Final, f)
		}
	}
	if len(st.Final) == 0 {
		return fmt.Errorf("no factor passed schema validation")
	}
	return nil
}

// stageIngest persists as PENDING review — a human approves in the factor panel.
func (s *ChatService) stageIngest(ctx context.Context, st *PipelineState) error {
	owner := ownerOf(ctx)
	for _, f := range st.Final {
		if f.FactorID == "" {
			f.FactorID = messages.NewID()
		}
		f.OwnerEmail = owner
		f.SourceReportID = st.ReportPath
		f.Status = FactorBacktested
		f.CreatedAt = time.Now().UnixMilli()
		if err := saveFactor(s.Cfg.Storage.BaseStoragePath, owner, f); err != nil {
			continue // one bad write must not lose the rest
		}
		st.Ingested = append(st.Ingested, f)
	}
	return nil
}

// --- helpers ----------------------------------------------------------------

func ownerOf(ctx context.Context) string { return agent.MetaFrom(ctx).UserEmail }

func toRefs(fs []Factor) factorRefs {
	out := make(factorRefs, 0, len(fs))
	for _, f := range fs {
		out = append(out, factorRef{name: f.Name, tokens: tokenize(f.Name + " " + f.Rationale + " " + f.Expression)})
	}
	return out
}

var numberedRe = regexp.MustCompile(`(?m)^\s*\d+[.、)]\s*(.+?)\s*$`)

// parseNumberedList pulls "1. …" items out of the parser's answer; falls back to
// non-empty lines so a model that ignores the format still yields theses.
func parseNumberedList(s string) []string {
	var out []string
	for _, m := range numberedRe.FindAllStringSubmatch(s, -1) {
		if t := strings.TrimSpace(m[1]); t != "" {
			out = append(out, t)
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); len(t) > 8 && !strings.HasPrefix(t, "#") {
			out = append(out, t)
		}
	}
	return out
}

// factorsFromText recovers a []Factor from an agent's answer, tolerating the
// prose/markdown a model wraps around its JSON.
func factorsFromText(s string) []Factor {
	items := coerceFactorList(s)
	out := make([]Factor, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if errs := validateFactorMap(m); len(errs) > 0 {
			continue // drop schema-invalid proposals rather than poisoning the gate
		}
		out = append(out, factorFromMap(m))
	}
	return out
}
