import { MetricsPanel, type MetricsRunContext } from './MetricsPanel';
import { BudgetFields } from './BudgetFields';
import type { RunBudgetLimits } from '../lib/runBudget';
import { useEffect, useState } from "react";
import { api, artifacts as artifactApi, files as fileApi } from "../api";
import type { Artifact, RunRecord } from "../types";
import { Icon, type IconName } from "./Icon";
import { PanelEmpty as Blank } from "./PanelEmpty";
import type { WorkbenchTab as Tab } from "../lib/workbenchTabs";
const ARTKIND_ICON: Record<string, string> = {
  pr_review: "🔀", architecture: "🗺️", incident: "🚨", checklist: "✅", audit: "🔍", custom: "📊",
};

export function DashboardPanel({ conversationID, onJumpToConversation, goTab, onOpenArtifact, budget, onBudgetChange, budgetDisabled, runContext }: { runContext?: MetricsRunContext; budget?: RunBudgetLimits; onBudgetChange?: (budget: RunBudgetLimits) => void; budgetDisabled?: boolean; conversationID: string; onJumpToConversation: (cid: string) => void; goTab: (t: Tab) => void; onOpenArtifact: (id: string) => void }) {
  const [runs, setRuns] = useState<RunRecord[]>([]);
  const [arts, setArts] = useState<Artifact[]>([]);
  const [fileCount, setFileCount] = useState(0);
  const [taskCount, setTaskCount] = useState(0);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let alive = true;
    Promise.all([
      api.listRuns({}).catch(() => ({ runs: [] })),
      artifactApi.list().then((r) => r.artifacts || []).catch(() => []),
      (conversationID ? fileApi.scopedList(".", conversationID, true).then((items) => items.filter((i) => !i.dir && !i.name.startsWith(".")).length) : Promise.resolve(0)).catch(() => 0),
      api.getTasks().then((r) => (r.tasks || []).filter((t) => t.cron_status === "on").length).catch(() => 0),
    ])
      .then(([r, a, fc, tc]) => { if (!alive) return; setRuns(r.runs || []); setArts(a); setFileCount(fc); setTaskCount(tc); })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [conversationID]);

  const budgetFields = budget && onBudgetChange ? <BudgetFields value={budget} onChange={onBudgetChange} disabled={budgetDisabled} /> : null;
  if (loading) return <div className="p-3">{budgetFields}<MetricsPanel runContext={runContext} /><Blank>加载中…</Blank></div>;

  // Workspace summary + recent pages render even before the first run, so the
  // panel reveals what's inside (pages / files / tasks) at a glance.
  const NavTile = ({ icon, label, value, onClick }: { icon: IconName; label: string; value: number; onClick: () => void }) => (
    <button onClick={onClick} className="group flex flex-col items-start rounded-xl border border-border bg-surface px-3 py-2.5 text-left transition hover:border-accent/40">
      <span className="inline-flex items-center gap-1.5 text-[11px] text-faint"><Icon name={icon} size={12} /> {label}</span>
      <span className="mt-0.5 text-[20px] font-semibold leading-tight text-ink group-hover:text-accent">{value}</span>
    </button>
  );

  const workspace = (
    <>
      {budgetFields}
      <MetricsPanel runContext={runContext} />
      <div className="grid grid-cols-3 gap-2">
        <NavTile icon="image" label="页面" value={arts.length} onClick={() => goTab("artifacts")} />
        <NavTile icon="folder" label="文件" value={fileCount} onClick={() => goTab("files")} />
        <NavTile icon="clock" label="定时任务" value={taskCount} onClick={() => goTab("tasks")} />
      </div>
      {arts.length > 0 && (
        <div className="rounded-xl border border-border bg-surface p-3">
          <div className="mb-2 flex items-center justify-between">
            <span className="text-[11px] font-medium uppercase tracking-wide text-faint">最近页面</span>
            <button onClick={() => goTab("artifacts")} className="text-[11px] text-faint hover:text-accent">全部 →</button>
          </div>
          <div className="space-y-1">
            {arts.slice(0, 3).map((a) => (
              <button key={a.artifact_id} onClick={() => onOpenArtifact(a.artifact_id)} className="flex w-full items-center gap-2 rounded-lg px-1.5 py-1.5 text-left hover:bg-surface2">
                <span className="text-[14px]">{ARTKIND_ICON[a.kind] || "📄"}</span>
                <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{a.title}</span>
                <span className="shrink-0 text-[11px] text-faint">v{a.current_version}</span>
              </button>
            ))}
          </div>
        </div>
      )}
    </>
  );

  if (runs.length === 0)
    return (
      <div className="space-y-3 p-3">
        {workspace}
        <div className="rounded-xl border border-dashed border-border p-5 text-center text-[12.5px] text-muted">还没有运行记录。跑一个任务后这里会出现执行统计。</div>
      </div>
    );

  const done = runs.filter((r) => r.status === "done").length;
  const failed = runs.filter((r) => r.status === "failed").length;
  // "partial" ran to an orderly stop without finishing the job (out of budget, or
  // plan steps left undone). It is an OUTCOME, so it belongs in the denominator —
  // counting it as success is exactly the flattery this status exists to prevent.
  const partial = runs.filter((r) => r.status === "partial").length;
  // "interrupted" means the serving process went away. Nothing was decided, so it
  // is not an outcome at all and must not drag the success rate down.
  const interrupted = runs.filter((r) => r.status === "interrupted").length;
  const finished = done + failed + partial;
  const successRate = finished ? Math.round((done / finished) * 100) : 0;
  const durs = runs.filter((r) => r.duration_ms > 0).map((r) => r.duration_ms);
  const avgDur = durs.length ? Math.round(durs.reduce((a, b) => a + b, 0) / durs.length / 1000) : 0;
  const triggers = runs.reduce((acc, r) => { const k = r.trigger || "manual"; acc[k] = (acc[k] || 0) + 1; return acc; }, {} as Record<string, number>);
  const fmtNum = (n: number) => (n >= 1000 ? (n / 1000).toFixed(1).replace(/\.0$/, "") + "k" : String(n));
  const dot = (s: string) =>
    s === "done" ? "var(--color-ok)"
    : s === "failed" ? "#e0695f"
    : s === "partial" ? "#d2761f"   // finished, but not the whole job
    : s === "running" ? "#e3b341"
    : "var(--color-faint)";          // interrupted / paused — no verdict
  const recent = runs.slice(0, 28).reverse();
  const TRIGGER_LABEL: Record<string, string> = { manual: "手动", schedule: "定时", workflow: "流程", webhook: "Webhook", rerun: "重跑", resume: "恢复" };

  const Stat = ({ label, value, sub }: { label: string; value: string; sub?: string }) => (
    <div className="rounded-xl border border-border bg-surface px-3 py-2.5">
      <div className="text-[11px] text-faint">{label}</div>
      <div className="mt-0.5 text-[20px] font-semibold leading-tight text-ink">{value}</div>
      {sub && <div className="mt-0.5 text-[11px] text-muted">{sub}</div>}
    </div>
  );

  return (
    <div className="space-y-3 p-3">
      {workspace}
      <div className="text-[11px] font-medium uppercase tracking-wide text-faint">运行概况</div>
      <Stat
        label="近期运行数"
        value={String(runs.length)}
        sub={[`${done} 成功`, partial ? `${partial} 部分` : "", `${failed} 失败`, interrupted ? `${interrupted} 中断` : ""].filter(Boolean).join(" · ")}
      />
      <div className="grid grid-cols-2 gap-2">
        <Stat label="成功率" value={successRate + "%"} sub={`${finished} 个有结论${interrupted ? ` · ${interrupted} 个中断不计` : ""}`} />
        <Stat label="平均耗时" value={avgDur + "s"} sub={durs.length ? `基于 ${durs.length} 个运行` : "—"} />
      </div>

      <div className="rounded-xl border border-border bg-surface p-3">
        <div className="mb-2 text-[11px] font-medium uppercase tracking-wide text-faint">近期运行 · {recent.length}</div>
        <div className="flex items-end gap-[3px]">
          {recent.map((r) => (
            <button
              key={r.run_id}
              onClick={() => r.conversation_id && onJumpToConversation(r.conversation_id)}
              title={`${r.status} · ${fmtNum(r.tokens || 0)} tok · ${(r.prompt || "").slice(0, 40)}`}
              className="h-7 flex-1 rounded-sm transition hover:opacity-70"
              style={{ background: dot(r.status), minWidth: 4 }}
            />
          ))}
        </div>
        <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-1 text-[10.5px] text-faint">
          <span><span style={{ color: "var(--color-ok)" }}>●</span> 成功</span>
          <span><span style={{ color: "#d2761f" }}>●</span> 部分完成</span>
          <span><span style={{ color: "#e0695f" }}>●</span> 失败</span>
          <span><span style={{ color: "#e3b341" }}>●</span> 进行中</span>
          <span><span style={{ color: "var(--color-faint)" }}>●</span> 中断</span>
        </div>
      </div>

      <div className="rounded-xl border border-border bg-surface p-3">
        <div className="mb-2 text-[11px] font-medium uppercase tracking-wide text-faint">触发来源</div>
        <div className="flex flex-wrap gap-1.5">
          {Object.entries(triggers).sort((a, b) => b[1] - a[1]).map(([k, v]) => (
            <span key={k} className="rounded-full bg-surface2 px-2 py-0.5 text-[12px] text-muted">
              {TRIGGER_LABEL[k] || k} · {v}
            </span>
          ))}
        </div>
      </div>

    </div>
  );
}
