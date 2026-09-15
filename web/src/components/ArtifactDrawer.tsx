import { useWorkbenchWidth } from '../hooks/useWorkbenchWidth';
import { ActionChip } from './ActionChip';
import type { RunBudgetLimits } from '../lib/runBudget';
import type { MetricsRunContext } from './MetricsPanel';
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { artifacts as artifactApi, files as fileApi } from "../api";
import { Icon, type IconName } from "./Icon";
import { ArtifactGallery, ArtifactPane } from "./Artifacts";

import type { RunsPanelProps } from './RunsPanel';
const SystemStatusPanel = lazy(() => import("./SystemStatusPanel").then(m => ({default:m.SystemStatusPanel})));
const RunsPanel = lazy(() => import('./RunsPanel'));
const MetricsPanel = lazy(() => import('./MetricsPanel').then(m => ({default:m.MetricsPanel})));
const WorkflowsPanel = lazy(() => import('./WorkflowsPanel').then(m => ({default:m.WorkflowsPanel})));
const ConnectorsPanel = lazy(() => import('./ConnectorsPanel').then(m => ({default:m.ConnectorsPanel})));
const FactorsPanel = lazy(() => import('./FactorsPanel').then(m => ({default:m.FactorsPanel})));
const TasksPanel = lazy(() => import('./TasksPanel').then(m => ({default:m.TasksPanel})));
import type { WorkbenchTab as Tab } from "../lib/workbenchTabs";
const DashboardPanel = lazy(() => import("./DashboardPanel").then(m => ({ default: m.DashboardPanel })));
const FilesPanel = lazy(() => import("./FilesPanel").then(m => ({ default: m.FilesPanel })));

// Each tab carries a one-line tip — the words 运行/流程/任务 are ambiguous on
// their own (execution log? workflow definition? schedule?), so the tooltip
// disambiguates them.
const TAB_META: Record<Tab, { label: string; tip: string }> = {
  overview: { label: "概览", tip: "工作区概览与近期活动" },
  artifacts: { label: "页面", tip: "实时、可分享的可视化页面(Artifacts)" },
  files: { label: "文件", tip: "工作区里的文件" },
  runs: { label: "运行", tip: "执行历史:每次任务运行的记录" },
  flows: { label: "流程", tip: "工作流定义:可复用的多步 DAG 管线" },
  tasks: { label: "任务", tip: "定时 / 触发的任务(调度与待办)" },
  factors: { label: "因子", tip: "量化因子库:研报 → 因子流水线的产出" },
  integrations: { label: "集成", tip: "外部工具 / MCP 连接器" },
  system: { label: "服务状态", tip: "运行版本与服务就绪状态" },
  metrics: { label: "指标", tip: "用量与性能指标" },
};
// Nine tabs is a back-office crammed into a chat sidebar. Collapse them into 4
// semantic FACES; multi-tab faces (舞台/运营台) get an inline sub-nav. runs/flows/
// tasks/integrations/metrics are all "execution & observability" → one 运营台.
type Face = "overview" | "stage" | "files" | "ops";
const FACES: { id: Face; label: string; tip: string; icon: IconName; subs: Tab[] }[] = [
  { id: "overview", label: "概览", tip: "工作区概览与近期活动", icon: "chart", subs: ["overview"] },
  { id: "stage", label: "成果", tip: "会话文件与可分享页面", icon: "folder", subs: ["files", "artifacts"] },
  { id: "ops", label: "运营台", tip: "执行与可观测:运行 / 流程 / 任务 / 因子 / 集成 / 指标", icon: "gear", subs: ["runs", "flows", "tasks", "factors", "integrations", "metrics", "system"] },
];
// Icons for the 运营台 sub-tabs, so the dense sub-nav scans at a glance.
const SUB_ICON: Partial<Record<Tab, IconName>> = {
  runs: "play", flows: "share", tasks: "clock", factors: "table", integrations: "plug", metrics: "chart",
};
function faceOf(tab: Tab): Face {
  return (FACES.find((f) => f.subs.includes(tab)) || FACES[0]).id;
}

export function ArtifactDrawer({
  open,
  onClose,
  tab,
  setTab,
  liveTab,
  email,
  conversationID,
  onJumpToConversation,
  focusArtifact,
  onClearArtifact,
  onResumeRun, isRunBusy, runRevision, canResumeRun,
  budget, onBudgetChange, budgetDisabled, runContext,
}: {
  budget?: RunBudgetLimits;
  onBudgetChange?: (budget: RunBudgetLimits) => void;
  budgetDisabled?: boolean;
  runContext?: MetricsRunContext;
  canResumeRun: RunsPanelProps["canResumeRun"];
  onResumeRun: RunsPanelProps["onResumeRun"]; isRunBusy: RunsPanelProps["isRunBusy"]; runRevision: number;
  open: boolean;
  onClose: () => void;
  tab: Tab;
  setTab: (t: Tab) => void;
  liveTab: Tab | null; // where the agent is working now (Live Focus)
  email: string;
  conversationID: string;
  onJumpToConversation: (cid: string) => void;
  focusArtifact: string | null; // artifact to open inline (from the in-chat card)
  onClearArtifact: () => void;
}) {
  const { width, separator } = useWorkbenchWidth(email);

  const [advanced, setAdvanced] = useState(false);
  const activeFace = faceOf(tab);
  const liveFace = liveTab ? faceOf(liveTab) : null;

  // The 页面 face shows the gallery, or a focused artifact rendered large/inline.
  const [focusArt, setFocusArt] = useState<string | null>(null);
  useEffect(() => {
    if (focusArtifact) { setFocusArt(focusArtifact); setTab("artifacts"); onClearArtifact(); }
  }, [focusArtifact, setTab, onClearArtifact]);

  // Live Focus: when the drawer is open and the agent moves to a new activity,
  // follow it (writing a file → 文件, browsing → 电脑, publishing → 页面). Only
  // reacts to *changes* in liveTab, so a manual click isn't immediately undone.
  const prevLive = useRef<Tab | null>(null);
  useEffect(() => {
    if (open && liveTab && liveTab !== prevLive.current) setTab(liveTab);
    prevLive.current = liveTab;
  }, [open, liveTab, setTab]);

  // Badge a tab that gains content (a new artifact/file) while you're elsewhere,
  // so you don't sit on 文件 and miss that 页面 just updated.
  const [newTabs, setNewTabs] = useState<Set<Tab>>(new Set());
  const seen = useRef<{ artifacts: number; files: number }>({ artifacts: -1, files: -1 });
  useEffect(() => {
    seen.current.files = -1;
    setNewTabs(prev => { const next = new Set(prev); next.delete("files"); return next; });
  }, [conversationID]);
  useEffect(() => {
    if (!open) return;
    let alive = true;
    const tick = async () => {
      const [a, f] = await Promise.all([
        artifactApi.list().then((r) => (r.artifacts || []).length).catch(() => -1),
        (conversationID ? fileApi.scopedList(".", conversationID, true).then((items) => items.filter((x) => !x.dir && !x.name.startsWith(".")).length) : Promise.resolve(0)).catch(() => -1),
      ]);
      if (!alive) return;
      setNewTabs((prev) => {
        const next = new Set(prev);
        if (seen.current.artifacts >= 0 && a > seen.current.artifacts && tab !== "artifacts") next.add("artifacts");
        if (seen.current.files >= 0 && f > seen.current.files && tab !== "files") next.add("files");
        return next;
      });
      seen.current = { artifacts: a, files: f };
    };
    tick();
    const id = setInterval(tick, 5000);
    return () => { alive = false; clearInterval(id); };
  }, [open, tab, conversationID]);
  // Clear a tab's badge once it's viewed.
  useEffect(() => {
    setNewTabs((prev) => { if (!prev.has(tab)) return prev; const n = new Set(prev); n.delete(tab); return n; });
  }, [tab]);

  // A face is "new" if any of its sub-tabs gained content; the LIVE face (where
  // the agent is working now) gets a pulsing ring so the drawer reads as a stage.
  const FaceBtn = (f: (typeof FACES)[number]) => {
    const on = f.id === activeFace;
    const live = f.id === liveFace;
    const dot = f.subs.some((s) => newTabs.has(s));
    return (
      <button
        key={f.id}
        onClick={() => setTab(f.subs[0])}
        title={live ? f.tip + " · Orka 正在这里工作" : f.tip}
        role="tab"
        aria-selected={on}
        className={
          "relative inline-flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-[13px] transition " +
          (on ? "bg-accentsoft text-accent" : "text-muted hover:bg-surface2") +
          (live && !on ? " ring-1 ring-accent/50" : "")
        }
      >
        {live ? (
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-ok" />
        ) : (
          <Icon name={f.icon} size={14} className={on ? "" : "text-faint"} />
        )}
        {f.label}
        {dot && !on && !live && <span className="absolute right-1 top-1 h-1.5 w-1.5 rounded-full bg-accent" />}
      </button>
    );
  };
  const SubBtn = (t: Tab) => (
    <button
      key={t}
      onClick={() => setTab(t)}
      title={TAB_META[t].tip}
      className={
        "relative inline-flex shrink-0 items-center gap-1 rounded-md px-2 py-1 text-[12px] transition " +
        (tab === t ? "bg-surface2 text-ink" : "text-faint hover:text-muted")
      }
    >
      {SUB_ICON[t] && <Icon name={SUB_ICON[t]!} size={12} />}
      {TAB_META[t].label}
      {newTabs.has(t) && tab !== t && <span className="absolute right-0 top-0 h-1.5 w-1.5 rounded-full bg-accent" />}
    </button>
  );

  return (
    <aside aria-label="工作台"
      style={{ width: open ? width : 0 }}
      className="fixed inset-y-0 right-0 z-40 shrink-0 overflow-hidden border-l border-border bg-surface md:relative md:z-auto"
    >
      {/* Pointer capture keeps the left edge reachable throughout a drag. */}
      <div {...separator} aria-controls="workbench-content"
        className="absolute left-0 top-0 z-10 h-full w-2 touch-none cursor-col-resize bg-transparent hover:bg-accent/20 focus-visible:bg-accent/20 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-accent/40"><span aria-hidden="true" className="pointer-events-none absolute left-[3px] top-1/2 h-10 w-0.5 -translate-y-1/2 rounded-full bg-border" /></div>
      <div className="flex h-full min-w-0 w-full flex-col pl-1">
        <div className="flex items-center gap-1 border-b border-border px-2 h-14">
          <div role="tablist" className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto no-scrollbar">
            {FACES.map(FaceBtn)}
          </div>
          <button
            onClick={onClose}
            className="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-faint hover:bg-surface2"
            title="Close"
            aria-label="关闭工件面板"
          >
            <Icon name="close" size={16} />
          </button>
        </div>
        {/* Sub-nav for multi-tab faces (舞台 / 运营台). */}
        {(FACES.find((f) => f.id === activeFace)?.subs.length ?? 0) > 1 && (
          <div className="flex min-w-0 flex-wrap items-center gap-1 border-b border-border bg-surface2/30 px-3 py-1.5">
            {FACES.find((f) => f.id === activeFace)!.subs.filter(t => activeFace !== "ops" || advanced || t === "runs").map(SubBtn)}
            {activeFace === "ops" && <ActionChip aria-expanded={advanced} onClick={() => setAdvanced(v => !v)}>专业功能 {advanced ? "收起" : "展开"}</ActionChip>}
          </div>
        )}
        <div id="workbench-content" className="min-h-0 min-w-0 flex-1 overflow-auto [overflow-wrap:anywhere]"><Suspense fallback={<p role="status" className="p-3 text-sm">正在加载面板…</p>}>
          {tab === "overview" && <DashboardPanel budget={budget} onBudgetChange={onBudgetChange} budgetDisabled={budgetDisabled} key={conversationID} conversationID={conversationID} onJumpToConversation={onJumpToConversation} goTab={setTab} onOpenArtifact={(id) => { setFocusArt(id); setTab("artifacts"); }} />}
          {tab === "artifacts" && (focusArt ? <ArtifactPane artifactId={focusArt} onBack={() => setFocusArt(null)} /> : <ArtifactGallery onOpen={setFocusArt} />)}
          {tab === "files" && <FilesPanel key={email + ":" + conversationID} email={email} conversationID={conversationID} />}
          {tab === "runs" && <RunsPanel onJumpToConversation={onJumpToConversation} onResumeRun={onResumeRun} isRunBusy={isRunBusy} runRevision={runRevision} canResumeRun={canResumeRun} />}
          {tab === "flows" && <WorkflowsPanel onJumpToConversation={onJumpToConversation} />}
          {tab === "integrations" && <ConnectorsPanel />}
          {tab === "system" && <SystemStatusPanel />}
          {tab === "metrics" && <MetricsPanel runContext={runContext} />}
          {tab === "tasks" && <TasksPanel onJumpToConversation={onJumpToConversation} />}
          {tab === "factors" && <FactorsPanel />}
        </Suspense></div>
      </div>
    </aside>
  );
}

// DashboardPanel is the at-a-glance overview: it aggregates the run history and
// live metrics into headline stats, a recent-activity strip, and trigger mix —
// so the platform's activity is visible without digging through the run list.
