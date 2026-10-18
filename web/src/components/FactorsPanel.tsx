import { useMemo, useState } from "react";
import { api } from "../api";
import type { Factor, WeightedPortfolio } from "../types";
import { toast } from "../lib/toast";
import { useResource, refreshResource } from "../lib/useResource";
import { PanelEmpty as Blank } from "./PanelEmpty";
const DIR_LABEL: Record<string,string> = {long:"做多",short:"做空",long_short:"多空"};
function AgreementTrend({ factors }: { factors: Factor[] }) {
  const series = useMemo(
    () =>
      factors
        .filter((f) => (f.agreement_score ?? 0) > 0)
        .sort((a, b) => a.created_at - b.created_at)
        .map((f) => f.agreement_score as number),
    [factors],
  );
  if (series.length < 2) return null;
  const avg = series.reduce((a, b) => a + b, 0) / series.length;
  const W = 220, H = 34, n = series.length;
  // y maps [0,1] agreement → chart; clamp domain to [0.5,1] for visible spread.
  const lo = 0.5, hi = 1;
  const x = (i: number) => (i / (n - 1)) * (W - 4) + 2;
  const y = (v: number) => H - 3 - ((Math.min(Math.max(v, lo), hi) - lo) / (hi - lo)) * (H - 6);
  const pts = series.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  const threshold = y(0.7);
  return (
    <div className="mb-2 rounded-xl border border-border bg-surface2/40 px-3 py-2">
      <div className="mb-1 flex items-center justify-between text-[11.5px]">
        <span className="text-faint">双盲一致性趋势 · {n} 个因子</span>
        <span className={avg >= 0.7 ? "text-ok" : "text-accent"}>均值 {(avg * 100).toFixed(0)}%</span>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" preserveAspectRatio="none" style={{ height: H }}>
        {/* 0.7 gate line */}
        <line x1={2} y1={threshold} x2={W - 2} y2={threshold} stroke="currentColor" className="text-border" strokeWidth={0.6} strokeDasharray="3 3" />
        <polyline points={pts} fill="none" stroke="currentColor" className="text-accent" strokeWidth={1.4} strokeLinejoin="round" strokeLinecap="round" />
        {series.map((v, i) => (
          <circle key={i} cx={x(i)} cy={y(v)} r={1.6} className={v >= 0.7 ? "text-ok" : "text-accent"} fill="currentColor" />
        ))}
      </svg>
    </div>
  );
}
const FACTOR_STATUS: Record<string, { label: string; cls: string }> = {
  approved: { label: "已入库", cls: "text-ok" },
  backtested: { label: "待审", cls: "text-muted" },
  proposed: { label: "提议", cls: "text-faint" },
  rejected: { label: "已拒", cls: "text-accent" },
  live: { label: "实盘", cls: "text-accent" },
};

export function FactorsPanel() {
  const all = useResource<Factor[]>("factors", () => api.listFactors().then((r) => r.factors || []), { interval: 6000 }) ?? [];
  const portfolios = useResource<WeightedPortfolio[]>("portfolios", () => api.listPortfolios().then((r) => r.portfolios || []), { interval: 12000 }) ?? [];
  const [running, setRunning] = useState(false);
  const [filter, setFilter] = useState<"all" | "backtested" | "approved">("all");
  const [busy, setBusy] = useState<string>(""); // factor_id being reviewed

  const pending = all.filter((f) => f.status === "backtested").length;
  const factors = filter === "all" ? all : all.filter((f) => f.status === filter);

  const review = async (id: string, status: "approved" | "rejected") => {
    setBusy(id);
    try {
      await api.setFactorStatus(id, status);
      toast(status === "approved" ? "已通过,入库" : "已拒绝", status === "approved" ? "success" : "info");
      refreshResource("factors");
    } catch {
      toast("操作失败,请重试", "error");
    } finally {
      setBusy("");
    }
  };

  const run = async () => {
    setRunning(true);
    try {
      const r = await api.runFactorPipeline();
      if (r.started > 0) toast(`已启动流水线,处理 ${r.started} 篇研报`, "success");
      else toast("工作区 reports/ 目录没有研报文件", "info");
      setTimeout(() => refreshResource("factors"), 1500);
    } catch {
      toast("启动流水线失败", "error");
    } finally {
      setRunning(false);
    }
  };

  const num = (v: number, d = 2) => (v >= 0 ? "+" : "") + v.toFixed(d);
  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[12px] text-faint">因子库 · {all.length}{pending > 0 && <span className="ml-1.5 text-accent">· {pending} 待审</span>}</span>
        <button onClick={run} disabled={running} className="rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40 disabled:opacity-50">
          {running ? "启动中…" : "⛁ 跑研报流水线"}
        </button>
      </div>
      {all.length > 0 && (
        <div className="mb-2 flex gap-1">
          {([["all", "全部"], ["backtested", "待审"], ["approved", "已入库"]] as const).map(([k, label]) => (
            <button
              key={k}
              onClick={() => setFilter(k)}
              className={"rounded-full px-2.5 py-0.5 text-[11.5px] transition " + (filter === k ? "bg-accentsoft text-accent" : "text-muted hover:bg-surface2")}
            >
              {label}
            </button>
          ))}
        </div>
      )}

      <AgreementTrend factors={all} />

      {factors.length === 0 && (
        <Blank icon="table" title="因子库还是空的" action={{ label: running ? "启动中…" : "⛁ 跑研报流水线", onClick: run }}>
          把研报(PDF / HTML / MD)放进工作区 <span className="font-mono">reports/</span> 目录再跑流水线:它会解析投资逻辑、双盲提取因子、回测打分,产出的因子会列在这里等你审核。
        </Blank>
      )}

      <div className="space-y-1.5">
        {factors.map((f) => {
          const st = FACTOR_STATUS[f.status] || { label: f.status, cls: "text-muted" };
          return (
            <div key={f.factor_id} className="rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
              <div className="flex items-center gap-2">
                <span className="text-[13px] font-medium text-ink">{f.name}</span>
                <span className="rounded-full bg-surface2 px-1.5 py-0.5 text-[10px] text-muted">{DIR_LABEL[f.direction] || f.direction}</span>
                <span className={"ml-auto text-[11px] " + st.cls}>● {st.label}</span>
              </div>
              {f.rationale && <div className="mt-1 line-clamp-1 text-[11.5px] text-faint" title={f.rationale}>{f.rationale}</div>}
              <div className="mt-1 break-all rounded-md bg-surface/70 px-2 py-1 font-mono text-[11px] text-muted">{f.expression}</div>
              <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-faint">
                <span title="信息系数">IC {num(f.metrics.ic, 3)}</span>
                <span title="信息比率">IR {num(f.metrics.ir)}</span>
                <span title="夏普">Sharpe {num(f.metrics.sharpe)}</span>
                <span title="换手率">换手 {(f.metrics.turnover * 100).toFixed(0)}%</span>
                <span title="最大回撤">回撤 {(f.metrics.max_dd * 100).toFixed(1)}%</span>
                {f.agreement_score != null && f.agreement_score > 0 && <span title="双盲一致性">一致性 {(f.agreement_score * 100).toFixed(0)}%</span>}
              </div>
              {f.status === "backtested" && (
                <div className="mt-2 flex gap-2">
                  <button onClick={() => review(f.factor_id, "approved")} disabled={busy === f.factor_id} className="rounded-lg bg-accent px-3 py-1 text-[12px] text-white hover:opacity-90 disabled:opacity-50">通过入库</button>
                  <button onClick={() => review(f.factor_id, "rejected")} disabled={busy === f.factor_id} className="rounded-lg border border-border bg-surface px-3 py-1 text-[12px] text-muted hover:border-accent/40 disabled:opacity-50">拒绝</button>
                </div>
              )}
            </div>
          );
        })}
      </div>

      {portfolios.length > 0 && (
        <>
          <div className="mb-2 mt-4 text-[12px] text-faint">加权组合 · {portfolios.length}</div>
          <div className="space-y-1.5">
            {portfolios.map((p) => (
              <div key={p.portfolio_id} className="rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
                <div className="flex items-center gap-2 text-[12.5px]">
                  <span className="font-medium text-ink">{p.method}</span>
                  <span className="text-faint">· {p.factor_ids.length} 因子</span>
                  <span className="ml-auto text-[11px] text-muted">IC {num(p.metrics.ic, 3)} · Sharpe {num(p.metrics.sharpe)}</span>
                </div>
              </div>
            ))}
          </div>
        </>
      )}
      <p className="mt-3 text-[11px] text-faint">因子由「研报 → 因子」流水线产出:解析投资逻辑 → 双盲提取 → GP 进化 + 回测 → 校验 → 人审 → 入库。</p>
    </div>
  );
}
