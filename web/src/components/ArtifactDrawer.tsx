import { useEffect, useMemo, useRef, useState } from "react";
import { api, artifacts as artifactApi, files as fileApi } from "../api";
import type { Artifact, Connector, Factor, MetricsSnapshot, RunRecord, TaskMeta, WeightedPortfolio, Workflow } from "../types";
import { toast } from "../lib/toast";
import { lineDiff, diffStats } from "../lib/diff";
import { useResource, refreshResource } from "../lib/useResource";
import { Icon, type IconName } from "./Icon";
import { FilePreview } from "./FilePreview";
import { ArtifactGallery, ArtifactPane } from "./Artifacts";

type Tab = "overview" | "artifacts" | "files" | "runs" | "tasks" | "flows" | "factors" | "integrations" | "metrics";

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
  metrics: { label: "指标", tip: "用量与性能指标" },
};
// Nine tabs is a back-office crammed into a chat sidebar. Collapse them into 4
// semantic FACES; multi-tab faces (舞台/运营台) get an inline sub-nav. runs/flows/
// tasks/integrations/metrics are all "execution & observability" → one 运营台.
type Face = "overview" | "stage" | "files" | "ops";
const FACES: { id: Face; label: string; tip: string; icon: IconName; subs: Tab[] }[] = [
  { id: "overview", label: "概览", tip: "工作区概览与近期活动", icon: "chart", subs: ["overview"] },
  { id: "stage", label: "页面", tip: "看 Orka 产出的可视化页面(Artifacts)", icon: "image", subs: ["artifacts"] },
  { id: "files", label: "文件", tip: "工作区里的文件", icon: "folder", subs: ["files"] },
  { id: "ops", label: "运营台", tip: "执行与可观测:运行 / 流程 / 任务 / 因子 / 集成 / 指标", icon: "gear", subs: ["runs", "flows", "tasks", "factors", "integrations", "metrics"] },
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
  onJumpToConversation,
  focusArtifact,
  onClearArtifact,
}: {
  open: boolean;
  onClose: () => void;
  tab: Tab;
  setTab: (t: Tab) => void;
  liveTab: Tab | null; // where the agent is working now (Live Focus)
  email: string;
  onJumpToConversation: (cid: string) => void;
  focusArtifact: string | null; // artifact to open inline (from the in-chat card)
  onClearArtifact: () => void;
}) {
  const [width, setWidth] = useState<number>(() => {
    const w = Number(localStorage.getItem("orka.drawerWidth"));
    return w >= 320 && w <= 760 ? w : 400;
  });
  const [dragging, setDragging] = useState(false);
  const [isDesktop, setIsDesktop] = useState(() => typeof window === "undefined" || window.innerWidth >= 768);
  useEffect(() => { localStorage.setItem("orka.drawerWidth", String(width)); }, [width]);
  useEffect(() => {
    const f = () => setIsDesktop(window.innerWidth >= 768);
    window.addEventListener("resize", f);
    return () => window.removeEventListener("resize", f);
  }, []);
  const effWidth = open ? (isDesktop ? width : Math.round(window.innerWidth * 0.86)) : 0;

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
    if (!open) return;
    let alive = true;
    const tick = async () => {
      const [a, f] = await Promise.all([
        artifactApi.list().then((r) => (r.artifacts || []).length).catch(() => -1),
        fileApi.list(".").then((items) => items.filter((x) => !x.dir && !x.name.startsWith(".")).length).catch(() => -1),
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
  }, [open, tab]);
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

  const startDrag = (e: React.MouseEvent) => {
    e.preventDefault();
    setDragging(true);
    const startX = e.clientX;
    const startW = width;
    const onMove = (ev: MouseEvent) => setWidth(Math.min(760, Math.max(320, startW + (startX - ev.clientX))));
    const onUp = () => {
      setDragging(false);
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  };

  return (
    <aside
      style={{ width: effWidth, transition: dragging ? "none" : "width 0.3s" }}
      className="fixed inset-y-0 right-0 z-40 shrink-0 overflow-hidden border-l border-border bg-surface md:static md:z-auto"
    >
      {/* desktop resize handle */}
      <div
        onMouseDown={startDrag}
        className="absolute left-0 top-0 z-10 hidden h-full w-1.5 cursor-col-resize hover:bg-accent/30 md:block"
        title="拖动调整宽度"
      />
      <div className="flex h-full w-full flex-col" style={{ minWidth: 280 }}>
        <div className="flex items-center gap-1 border-b border-border px-2 h-14">
          <div role="tablist" className="flex flex-1 items-center gap-0.5 overflow-x-auto no-scrollbar">
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
          <div className="flex items-center gap-1 border-b border-border bg-surface2/30 px-3 py-1.5">
            {FACES.find((f) => f.id === activeFace)!.subs.map(SubBtn)}
          </div>
        )}
        <div className="flex-1 overflow-y-auto">
          {tab === "overview" && <DashboardPanel onJumpToConversation={onJumpToConversation} goTab={setTab} onOpenArtifact={(id) => { setFocusArt(id); setTab("artifacts"); }} />}
          {tab === "artifacts" && (focusArt ? <ArtifactPane artifactId={focusArt} onBack={() => setFocusArt(null)} /> : <ArtifactGallery onOpen={setFocusArt} />)}
          {tab === "files" && <FilesPanel email={email} />}
          {tab === "runs" && <RunsPanel onJumpToConversation={onJumpToConversation} />}
          {tab === "flows" && <WorkflowsPanel onJumpToConversation={onJumpToConversation} />}
          {tab === "integrations" && <ConnectorsPanel />}
          {tab === "metrics" && <MetricsPanel />}
          {tab === "tasks" && <TasksPanel onJumpToConversation={onJumpToConversation} />}
          {tab === "factors" && <FactorsPanel />}
        </div>
      </div>
    </aside>
  );
}

// DashboardPanel is the at-a-glance overview: it aggregates the run history and
// live metrics into headline stats, a recent-activity strip, and trigger mix —
// so the platform's activity is visible without digging through the run list.
const ARTKIND_ICON: Record<string, string> = {
  pr_review: "🔀", architecture: "🗺️", incident: "🚨", checklist: "✅", audit: "🔍", custom: "📊",
};

function DashboardPanel({ onJumpToConversation, goTab, onOpenArtifact }: { onJumpToConversation: (cid: string) => void; goTab: (t: Tab) => void; onOpenArtifact: (id: string) => void }) {
  const [runs, setRuns] = useState<RunRecord[]>([]);
  const [metrics, setMetrics] = useState<MetricsSnapshot | null>(null);
  const [arts, setArts] = useState<Artifact[]>([]);
  const [fileCount, setFileCount] = useState(0);
  const [taskCount, setTaskCount] = useState(0);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    Promise.all([
      api.listRuns({}).catch(() => ({ runs: [] })),
      api.metrics().catch(() => null),
      artifactApi.list().then((r) => r.artifacts || []).catch(() => []),
      fileApi.list(".").then((items) => items.filter((i) => !i.dir && !i.name.startsWith(".")).length).catch(() => 0),
      api.getTasks().then((r) => (r.tasks || []).filter((t) => t.cron_status === "on").length).catch(() => 0),
    ])
      .then(([r, m, a, fc, tc]) => { setRuns(r.runs || []); setMetrics(m); setArts(a); setFileCount(fc); setTaskCount(tc); })
      .finally(() => setLoading(false));
  }, []);

  if (loading) return <Blank>加载中…</Blank>;

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
  const totalTokens = runs.reduce((a, r) => a + (r.tokens || 0), 0);
  const totalTools = runs.reduce((a, r) => a + (r.tool_calls || 0), 0);
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
      <div className="grid grid-cols-2 gap-2">
        <Stat
          label="总运行"
          value={String(runs.length)}
          sub={[`${done} 成功`, partial ? `${partial} 部分` : "", `${failed} 失败`, interrupted ? `${interrupted} 中断` : ""].filter(Boolean).join(" · ")}
        />
        <Stat label="成功率" value={successRate + "%"} sub={`${finished} 个有结论${interrupted ? ` · ${interrupted} 个中断不计` : ""}`} />
        <Stat label="累计 Token" value={fmtNum(totalTokens)} sub={`${fmtNum(totalTools)} 次工具调用`} />
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

      {metrics && (
        <div className="rounded-xl border border-border bg-surface p-3 text-[12px] text-muted">
          <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-faint">实时指标</div>
          <div className="grid grid-cols-2 gap-y-1">
            <span>活跃会话 <b className="text-ink">{metrics.active_sessions}</b></span>
            <span>检查点 <b className="text-ink">{metrics.checkpoints}</b></span>
            <span>LLM 调用 <b className="text-ink">{metrics.llm_calls}</b></span>
            <span>工具调用 <b className="text-ink">{metrics.tool_calls}</b></span>
          </div>
        </div>
      )}
    </div>
  );
}

// File "kinds" group the flat workspace into tidy, labelled sections so a busy
// workspace reads as 文档 / 代码 / 数据 / 图片 / 其他 instead of one long dump.
const FILE_KINDS: { id: string; icon: string; label: string; color: string; exts?: string[] }[] = [
  { id: "folder", icon: "📁", label: "文件夹", color: "text-[#c79a5a]" },
  { id: "doc", icon: "📝", label: "文档 / 报告", color: "text-[#5a86c7]", exts: ["md", "markdown", "txt", "doc", "docx", "rtf"] },
  { id: "pdf", icon: "📕", label: "PDF", color: "text-[#d06363]", exts: ["pdf"] },
  { id: "code", icon: "🧑‍💻", label: "代码", color: "text-[#7a9a6a]", exts: ["py", "js", "jsx", "ts", "tsx", "go", "java", "c", "h", "cpp", "rs", "sh", "rb", "php", "html", "css", "yaml", "yml", "sql"] },
  { id: "data", icon: "📊", label: "数据 / 表格", color: "text-[#5aa48a]", exts: ["json", "csv", "tsv", "xml", "ndjson", "xlsx", "xls"] },
  { id: "image", icon: "🖼️", label: "图片", color: "text-[#b07ac7]", exts: ["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp"] },
  { id: "slides", icon: "📑", label: "演示", color: "text-[#c78a5a]", exts: ["pptx", "ppt", "key"] },
  { id: "other", icon: "🗂️", label: "其他", color: "text-faint" },
];
function kindOf(name: string, dir: boolean): string {
  if (dir) return "folder";
  const ext = name.split(".").pop()?.toLowerCase() || "";
  for (const k of FILE_KINDS) if (k.exts?.includes(ext)) return k.id;
  return "other";
}
function kindMeta(name: string, dir: boolean) {
  const id = kindOf(name, dir);
  return FILE_KINDS.find((k) => k.id === id)!;
}

// dupKey normalizes a filename to spot likely-duplicate artifacts the agent may
// have generated several times: drop the extension, lowercase, strip separators
// and trailing language/version/date suffixes so e.g. "Report_EN" /
// "report-en-v2" / "ai_agent_research" / "ai-agent-research" collapse together.
function dupKey(name: string, dir: boolean): string {
  let s = dir ? name : name.replace(/\.[^.]+$/, "");
  s = s.toLowerCase().replace(/[\s_\-.]+/g, "");
  s = s.replace(/(business|analysis|report|经营分析|报告|en|zh|cn|final|copy|v?\d{1,4}|\d{6,8})/g, "");
  return s;
}

type FileItem = { name: string; dir: boolean; size: number; mtime?: number };
type SortKey = "name" | "time" | "size";

// Runtime junk the sandbox leaves in the workspace (HOME=root → caches, python
// bytecode) — hidden by default so the panel shows the user's actual files.
const JUNK = new Set(["__pycache__", "Library", "node_modules", ".cache", ".config", ".local", ".orka_trash", ".npm", ".ipynb_checkpoints"]);
function isHidden(name: string): boolean {
  return name.startsWith(".") || JUNK.has(name);
}

function FilesPanel({ email }: { email: string }) {
  const [items, setItems] = useState<FileItem[]>([]);
  const [pct, setPct] = useState<number | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [showHidden, setShowHidden] = useState(false);
  const [sort, setSort] = useState<SortKey>("name");
  const inputRef = useRef<HTMLInputElement>(null);
  const refresh = () => fileApi.list(".").then(setItems).catch(() => setItems([]));
  useEffect(() => {
    refresh(); /* eslint-disable-next-line */
  }, [email]);
  const onUpload = async (f: File) => {
    setPct(0);
    try {
      await fileApi.upload(f, "", setPct);
      await refresh();
    } finally {
      setPct(null);
    }
  };
  const del = (name: string) => fileApi.delete(name).then(refresh);

  const q = query.trim().toLowerCase();
  const hiddenCount = items.filter((it) => isHidden(it.name)).length;
  const cmp =
    sort === "time"
      ? (a: FileItem, b: FileItem) => (b.mtime || 0) - (a.mtime || 0)
      : sort === "size"
        ? (a: FileItem, b: FileItem) => b.size - a.size
        : (a: FileItem, b: FileItem) => a.name.localeCompare(b.name);
  const filtered = items
    .filter((it) => showHidden || !isHidden(it.name))
    .filter((it) => !q || it.name.toLowerCase().includes(q))
    .sort(cmp);
  const grouped = FILE_KINDS.map((k) => ({ ...k, files: filtered.filter((it) => kindOf(it.name, it.dir) === k.id) })).filter(
    (g) => g.files.length > 0,
  );

  // Flag likely-duplicate files (same normalized stem) so a workspace full of
  // near-identical agent exports is legible at a glance.
  const dupSiblings = new Map<string, string[]>();
  {
    const byKey = new Map<string, string[]>();
    for (const it of filtered) {
      const k = dupKey(it.name, it.dir);
      if (k.length < 3) continue;
      (byKey.get(k) ?? byKey.set(k, []).get(k)!).push(it.name);
    }
    for (const names of byKey.values()) if (names.length > 1) for (const n of names) dupSiblings.set(n, names.filter((x) => x !== n));
  }

  const Row = (it: FileItem) => {
    const meta = kindMeta(it.name, it.dir);
    const dups = dupSiblings.get(it.name);
    return (
    <div key={it.name} className="group flex items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface2">
      <span className={meta.color}>{meta.icon}</span>
      {it.dir ? (
        <span className="flex-1 truncate text-[14px] text-ink">{it.name}</span>
      ) : (
        <button onClick={() => setPreview(it.name)} className="flex-1 truncate text-left text-[14px] text-ink hover:text-accent" title="预览">
          {it.name}
        </button>
      )}
      {dups && dups.length > 0 && (
        <span className="shrink-0 text-[11px] text-[#c79a5a] group-hover:hidden" title={"可能与以下文件重复:\n" + dups.join("\n")}>🔁</span>
      )}
      <span className="text-[11px] text-faint">{fmtBytes(it.size)}</span>
      {!it.dir && (
        <a href={fileApi.downloadURL(it.name)} className="text-accent opacity-0 group-hover:opacity-100" aria-label={"下载 " + it.name}>
          <Icon name="download" size={13} />
        </a>
      )}
      <button onClick={() => del(it.name)} className="text-faint opacity-0 hover:text-accent group-hover:opacity-100" aria-label={"删除 " + it.name}>
        <Icon name="trash" size={13} />
      </button>
    </div>
    );
  };

  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="inline-flex items-center gap-1.5 truncate text-[12px] text-faint" title={email}><Icon name="folder" size={13} /> 我的文件</span>
        <button
          onClick={() => inputRef.current?.click()}
          className="shrink-0 rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40"
        >
          Upload
        </button>
        <input ref={inputRef} type="file" hidden onChange={(e) => e.target.files?.[0] && onUpload(e.target.files[0])} />
      </div>
      {items.length > 6 && (
        <div className="mb-2 flex items-center gap-1.5">
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="筛选文件…"
            className="min-w-0 flex-1 rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[12.5px] outline-none focus:border-accent/50"
          />
          <select
            value={sort}
            onChange={(e) => setSort(e.target.value as SortKey)}
            className="shrink-0 rounded-lg border border-border bg-surface px-1.5 py-1.5 text-[12px] text-muted outline-none"
            title="排序方式"
          >
            <option value="name">名称</option>
            <option value="time">时间</option>
            <option value="size">大小</option>
          </select>
        </div>
      )}
      {hiddenCount > 0 && (
        <button
          onClick={() => setShowHidden((v) => !v)}
          className="mb-2 text-[11.5px] text-faint hover:text-accent"
          title="系统缓存 / 隐藏文件(__pycache__、.cache 等)"
        >
          {showHidden ? "隐藏" : "显示"}系统文件 · {hiddenCount}
        </button>
      )}
      {pct !== null && (
        <div className="mb-2 h-1 w-full overflow-hidden rounded bg-surface2">
          <div className="h-full bg-accent transition-all" style={{ width: pct + "%" }} />
        </div>
      )}
      {filtered.length === 0 && (
        items.length === 0
          ? <Blank icon="folder" title="工作区还是空的">Orka 产出的文件(报告、图表、脚本、导出的文档)都会落在这里,你也可以直接上传文件让它读取。</Blank>
          : <Blank icon="search" title="没有匹配的文件">换个关键词试试,或清空搜索框查看全部。</Blank>
      )}
      {grouped.map((g) => (
        <div key={g.id} className="mb-3">
          <div className="mb-1 px-1 text-[11px] font-medium uppercase tracking-wide text-faint">
            {g.icon} {g.label} · {g.files.length}
          </div>
          <div className="space-y-0.5">{g.files.map(Row)}</div>
        </div>
      ))}
      {preview && <FilePreview name={preview} onClose={() => setPreview(null)} />}
    </div>
  );
}

function MetricsPanel() {
  // Shares the same "metrics" channel as the header chip — no duplicate poll.
  const m = useResource("metrics", api.metrics, { interval: 4000 }) ?? null;
  const fmt = (n: number) => (n >= 1000 ? (n / 1000).toFixed(1) + "k" : String(n));
  const stats = [
    { k: "运行中会话", v: m?.active_sessions ?? 0 },
    { k: "Checkpoints", v: m?.checkpoints ?? 0 },
    { k: "工具调用", v: m?.tool_calls ?? 0 },
    { k: "平均工具 µs", v: Math.round(m?.avg_tool_call_micros ?? 0) },
    { k: "LLM 调用", v: m?.llm_calls ?? 0 },
    { k: "总 tokens", v: fmt(m?.total_tokens ?? 0) },
    { k: "输入 tokens", v: fmt(m?.prompt_tokens ?? 0) },
    { k: "输出 tokens", v: fmt(m?.completion_tokens ?? 0) },
  ];
  return (
    <div className="grid grid-cols-2 gap-2.5 p-3">
      {stats.map((s) => (
        <div key={s.k} className="rounded-xl border border-border bg-surface2/50 p-3.5">
          <div className="font-serif text-[26px] text-ink">{s.v}</div>
          <div className="mt-1 text-[12px] text-faint">{s.k}</div>
        </div>
      ))}
    </div>
  );
}

const RUN_COLOR: Record<string, string> = {
  running: "text-accent", done: "text-ok", failed: "text-accent", paused: "text-muted", start: "text-muted",
};
const INTERVALS = [
  { label: "每分钟", sec: 60 },
  { label: "每 10 分钟", sec: 600 },
  { label: "每小时", sec: 3600 },
  { label: "每天", sec: 86400 },
];

function taskPrompt(t: TaskMeta): string {
  const v = (t.variables || {}) as Record<string, unknown>;
  return String(v.prompt_template || v.prompt || v.title || "—");
}
function fmtWhen(ms?: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

// WorkflowsPanel: define + run multi-step pipelines as a DAG. Each step has a
// prompt plus optional dependencies (the DAG edges — independent steps run in
// parallel), a run-if guard, and an on-error policy. Steps can reference a
// prior step's output with {{step_name}}.
type DraftStep = { name: string; prompt: string; depends_on: string[]; run_if: string; on_error: string };
const emptyStep = (i: number): DraftStep => ({ name: `step${i + 1}`, prompt: "", depends_on: [], run_if: "", on_error: "stop" });

function WorkflowsPanel({ onJumpToConversation }: { onJumpToConversation: (cid: string) => void }) {
  const [flows, setFlows] = useState<Workflow[]>([]);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [steps, setSteps] = useState<DraftStep[]>([emptyStep(0)]);
  const refresh = () => api.listWorkflows().then((r) => setFlows(r.workflows || [])).catch(() => {});
  useEffect(() => { refresh(); }, []);

  const setStep = (i: number, patch: Partial<DraftStep>) => setSteps((ss) => ss.map((s, j) => (j === i ? { ...s, ...patch } : s)));
  const reset = () => { setName(""); setSteps([emptyStep(0)]); setAdding(false); };

  const save = async () => {
    const clean = steps
      .filter((s) => s.prompt.trim())
      .map((s) => ({
        name: s.name.trim() || "step",
        prompt: s.prompt.trim(),
        depends_on: s.depends_on.filter(Boolean),
        run_if: s.run_if.trim() || undefined,
        on_error: s.on_error !== "stop" ? s.on_error : undefined,
      }));
    if (!name.trim() || clean.length === 0) return;
    try {
      await api.createWorkflow(name.trim(), clean);
    } catch {
      toast("流程创建失败,请重试", "error");
      return; // keep the form so the user doesn't lose their steps
    }
    reset(); refresh();
  };
  const run = async (id: string) => {
    let r;
    try {
      r = await api.runWorkflow(id);
    } catch {
      toast("流程启动失败,请重试", "error");
      return;
    }
    if (r?.conversation_id) { toast("流程已启动", "success"); onJumpToConversation(r.conversation_id); }
    else toast("流程已启动,但未返回会话", "info");
  };

  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[12px] text-faint">工作流 · {flows.length}</span>
        <button onClick={() => (adding ? reset() : setAdding(true))} className="rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40">
          {adding ? "取消" : "+ 新建流程"}
        </button>
      </div>
      {adding && (
        <div className="mb-3 space-y-2 rounded-xl border border-border bg-surface2/40 p-3">
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="流程名称，如 每日竞品简报" className="w-full rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] outline-none focus:border-accent/50" />
          {steps.map((s, i) => {
            const priors = steps.slice(0, i).map((p) => p.name).filter(Boolean);
            return (
              <div key={i} className="space-y-1.5 rounded-lg border border-border/70 bg-surface p-2">
                <div className="flex items-center gap-1.5">
                  <span className="text-[11px] text-faint">#{i + 1}</span>
                  <input value={s.name} onChange={(e) => setStep(i, { name: e.target.value.replace(/\s+/g, "_") })} placeholder="步骤名" className="w-24 rounded border border-border bg-surface2 px-1.5 py-0.5 text-[12px] outline-none" />
                  {steps.length > 1 && <button onClick={() => setSteps((ss) => ss.filter((_, j) => j !== i))} className="ml-auto text-faint hover:text-accent" aria-label="移除步骤"><Icon name="close" size={13} /></button>}
                </div>
                <textarea value={s.prompt} onChange={(e) => setStep(i, { prompt: e.target.value })} rows={2} placeholder={'该步要做什么(可用 {{前一步名}} 引用其输出)'} className="w-full resize-none rounded border border-border bg-surface2 px-2 py-1 text-[12.5px] outline-none focus:border-accent/50" />
                {priors.length > 0 && (
                  <div className="flex flex-wrap items-center gap-1 text-[11px]">
                    <span className="text-faint">依赖:</span>
                    {priors.map((p) => {
                      const on = s.depends_on.includes(p);
                      return (
                        <button key={p} onClick={() => setStep(i, { depends_on: on ? s.depends_on.filter((d) => d !== p) : [...s.depends_on, p] })}
                          className={"rounded-full border px-1.5 py-0.5 " + (on ? "border-accent/40 bg-accentsoft text-accent" : "border-border text-faint")}>
                          {p}
                        </button>
                      );
                    })}
                    <span className="text-faint/60">空=接在上一步后</span>
                  </div>
                )}
                <div className="flex items-center gap-1.5">
                  <input value={s.run_if} onChange={(e) => setStep(i, { run_if: e.target.value })} placeholder="条件(可选)，如 step1 contains FOUND" className="min-w-0 flex-1 rounded border border-border bg-surface2 px-1.5 py-0.5 text-[11.5px] outline-none" />
                  <select value={s.on_error} onChange={(e) => setStep(i, { on_error: e.target.value })} className="rounded border border-border bg-surface2 px-1 py-0.5 text-[11.5px] text-muted outline-none" title="出错时">
                    <option value="stop">出错停止</option>
                    <option value="continue">出错继续</option>
                    <option value="retry:2">重试 2 次</option>
                    <option value="retry:3">重试 3 次</option>
                  </select>
                </div>
              </div>
            );
          })}
          <button onClick={() => setSteps((ss) => [...ss, emptyStep(ss.length)])} className="w-full rounded-lg border border-dashed border-border py-1 text-[12px] text-faint hover:border-accent/40 hover:text-accent">+ 添加步骤</button>
          <button onClick={save} disabled={!name.trim() || !steps.some((s) => s.prompt.trim())} className="w-full rounded-lg bg-accent px-3 py-1.5 text-[13px] text-white disabled:opacity-40">保存流程</button>
        </div>
      )}
      {flows.length === 0 && !adding && (
        <Blank icon="share" title="还没有工作流" action={{ label: "+ 新建流程", onClick: () => setAdding(true) }}>
          把一件多步骤的事拆成几步并保存下来:可以设步骤依赖、条件跳过和失败重试,Orka 按 DAG 执行,之后一键复跑。
        </Blank>
      )}
      <div className="space-y-1.5">
        {flows.map((wf) => (
          <div key={wf.workflow_id} className="rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
            <div className="flex items-center gap-2">
              <span className="text-[15px]">🧩</span>
              <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{wf.name}</span>
              <span className="text-[11px] text-faint">{wf.steps.length} 步</span>
            </div>
            <ol className="mt-1 ml-5 list-decimal text-[11px] text-muted">
              {wf.steps.slice(0, 5).map((s, i) => (
                <li key={i} className="truncate">
                  <span className="text-ink/80">{s.name}</span>
                  {(s.depends_on?.length ?? 0) > 0 && <span className="text-faint"> ←{s.depends_on!.join(",")}</span>}
                  {s.run_if && <span className="text-accent"> ?{s.run_if}</span>}
                  <span className="text-faint"> · {s.prompt}</span>
                </li>
              ))}
            </ol>
            <div className="mt-1.5 flex items-center gap-3 text-[11px]">
              <button onClick={() => run(wf.workflow_id)} className="inline-flex items-center gap-1 text-accent hover:underline"><Icon name="play" size={11} /> 运行</button>
              <button onClick={() => api.deleteWorkflow(wf.workflow_id).then(refresh)} className="text-faint hover:text-accent">删除</button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

// parseHeaders turns "Key: Value" lines into a header object.
function parseHeaders(text: string): Record<string, string> {
  const h: Record<string, string> = {};
  for (const ln of text.split("\n")) {
    const i = ln.indexOf(":");
    if (i > 0) h[ln.slice(0, i).trim()] = ln.slice(i + 1).trim();
  }
  return h;
}

// ConnectorsPanel manages external MCP servers — Orka's gateway can drive any of
// them, so this turns the agent into an integration platform.
function ConnectorsPanel() {
  const [conns, setConns] = useState<Connector[]>([]);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [transport, setTransport] = useState("streamable_http");
  const [url, setUrl] = useState("");
  const [headers, setHeaders] = useState("");
  const [probe, setProbe] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = () => api.listConnectors().then((r) => setConns(r.connectors || [])).catch(() => {});
  useEffect(() => { refresh(); }, []);

  const draft = () => ({ name, transport, url, headers: parseHeaders(headers) });
  const test = async () => {
    setBusy(true);
    setProbe(null);
    try {
      const r = await api.testConnector(draft());
      setProbe(r.ok ? `✓ 连接成功，发现 ${r.tools?.length || 0} 个工具` : `✕ ${r.error}`);
    } catch {
      setProbe("✕ 测试失败");
    } finally {
      setBusy(false);
    }
  };
  const save = async () => {
    if (!name.trim() || !url.trim()) return;
    setBusy(true);
    try {
      await api.createConnector(draft());
      setName(""); setUrl(""); setHeaders(""); setProbe(null); setAdding(false);
      toast("已添加集成，工具将在下次运行可用", "success");
      refresh();
    } finally { setBusy(false); }
  };

  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[12px] text-faint">外部 MCP 集成 · {conns.length}</span>
        <button onClick={() => setAdding((o) => !o)} className="rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40">
          {adding ? "取消" : "+ 添加集成"}
        </button>
      </div>

      {adding && (
        <div className="mb-3 space-y-2 rounded-xl border border-border bg-surface2/40 p-3">
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="名称，如 GitHub" className="w-full rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] outline-none focus:border-accent/50" />
          <div className="flex gap-2">
            <select value={transport} onChange={(e) => setTransport(e.target.value)} className="rounded-lg border border-border bg-surface px-2 py-1.5 text-[13px] outline-none">
              <option value="streamable_http">streamable_http</option>
              <option value="http">http (SSE)</option>
            </select>
            <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://mcp.example.com/sse" className="flex-1 rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] outline-none focus:border-accent/50" />
          </div>
          <textarea value={headers} onChange={(e) => setHeaders(e.target.value)} rows={2} placeholder="鉴权头，每行 Key: Value，如&#10;Authorization: Bearer sk-..." className="w-full resize-none rounded-lg border border-border bg-surface px-2.5 py-2 font-mono text-[12px] outline-none focus:border-accent/50" />
          {probe && <div className={"text-[12px] " + (probe.startsWith("✓") ? "text-ok" : "text-accent")}>{probe}</div>}
          <div className="flex items-center gap-2">
            <button onClick={test} disabled={busy || !url.trim()} className="rounded-lg border border-border px-2.5 py-1.5 text-[13px] text-muted hover:border-accent/40 disabled:opacity-40">测试连接</button>
            <button onClick={save} disabled={busy || !name.trim() || !url.trim()} className="ml-auto rounded-lg bg-accent px-3 py-1.5 text-[13px] text-white disabled:opacity-40">添加</button>
          </div>
        </div>
      )}

      {conns.length === 0 && !adding && (
        <Blank icon="plug" title="还没有外部工具" action={{ label: "+ 添加连接器", onClick: () => setAdding(true) }}>
          接入一个 MCP 服务后,它提供的工具会自动出现在 Orka 的工具表里,和内置工具一样被调用。
        </Blank>
      )}
      <div className="space-y-1.5">
        {conns.map((cn) => (
          <div key={cn.connector_id} className="flex items-center gap-2 rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
            <Icon name="plug" size={15} className="text-muted" />
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] text-ink">{cn.name}</div>
              <div className="truncate text-[11px] text-faint">{cn.transport} · {cn.url}</div>
            </div>
            <button onClick={() => api.deleteConnector(cn.connector_id).then(refresh)} aria-label="移除集成" className="text-faint hover:text-accent"><Icon name="close" size={13} /></button>
          </div>
        ))}
      </div>
    </div>
  );
}

const RUN_STATUS: Record<string, { label: string; cls: string }> = {
  running: { label: "运行中", cls: "text-accent" },
  done: { label: "完成", cls: "text-ok" },
  partial: { label: "部分完成", cls: "text-[#d2761f]" },
  failed: { label: "失败", cls: "text-accent" },
  paused: { label: "等待澄清", cls: "text-muted" },
  interrupted: { label: "已中断", cls: "text-faint" },
};

// BUDGET_REASON explains a run that stopped short. Naming the exhausted
// dimension is the difference between "it gave up" and "it hit a ceiling you
// can raise", which is the actionable version.
const BUDGET_REASON: Record<string, string> = {
  steps: "达到步数上限",
  tokens: "达到 token 上限",
  time: "达到时间上限",
};
const TRIGGER_LABEL: Record<string, string> = {
  manual: "手动", schedule: "定时", resume: "续跑", rerun: "重跑",
};

function fmtDur(ms: number): string {
  if (!ms || ms < 0) return "—";
  if (ms < 1000) return ms + "ms";
  if (ms < 60000) return (ms / 1000).toFixed(1) + "s";
  return Math.floor(ms / 60000) + "m" + Math.round((ms % 60000) / 1000) + "s";
}

// RunsPanel is the execution history — the automation platform's audit log.
// Every run (manual / scheduled / rerun) lands here with status, duration,
// tokens, tool count; click to open its conversation, or re-run it.
function RunsPanel({ onJumpToConversation }: { onJumpToConversation: (cid: string) => void }) {
  const [onlyFailed, setOnlyFailed] = useState(false);
  const [diffOpen, setDiffOpen] = useState<string | null>(null); // run_id whose diff is expanded
  const runs =
    useResource<RunRecord[]>(
      "runs:" + (onlyFailed ? "failed" : "all"),
      () => api.listRuns(onlyFailed ? { status: "failed" } : {}).then((r) => r.runs || []),
      { interval: 5000 },
    ) ?? [];

  // For each run, find the previous run of the SAME task (the recurring scheduled
  // job), so a scheduled run can be diffed against "what it produced last time".
  // The list is newest-first, so the previous run is the next same-task entry.
  const prevOf = useMemo(() => {
    const m = new Map<string, RunRecord>();
    for (let i = 0; i < runs.length; i++) {
      const r = runs[i];
      if (!r.task_id) continue;
      for (let j = i + 1; j < runs.length; j++) {
        if (runs[j].task_id === r.task_id && runs[j].run_id !== r.run_id) {
          m.set(r.run_id, runs[j]);
          break;
        }
      }
    }
    return m;
  }, [runs]);

  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[12px] text-faint">运行历史 · {runs.length}</span>
        <button
          onClick={() => setOnlyFailed((o) => !o)}
          className={"rounded-lg border px-2.5 py-1 text-[12px] transition " + (onlyFailed ? "border-accent/40 bg-accentsoft text-accent" : "border-border text-muted hover:border-accent/40")}
        >
          只看失败
        </button>
      </div>
      {runs.length === 0 && (
        <Blank icon="play" title={onlyFailed ? "没有失败的运行" : "还没有运行记录"}>
          {onlyFailed
            ? "所有运行都成功了。取消筛选可以看到全部记录。"
            : "每次任务执行(手动、定时或流程触发)都会记在这里,含耗时、token、工具调用与最终结果,可重跑或跳回对话。"}
        </Blank>
      )}
      <div className="space-y-1.5">
        {runs.map((r) => {
          const st = RUN_STATUS[r.status] || { label: r.status, cls: "text-muted" };
          return (
            <div key={r.run_id} className="rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
              <div className="flex items-center gap-2">
                <span className={"text-[11px] font-medium " + st.cls}>● {st.label}</span>
                <span className="rounded-full bg-surface2 px-1.5 py-0.5 text-[10px] text-muted">{TRIGGER_LABEL[r.trigger] || r.trigger}</span>
                <span className="ml-auto text-[10px] text-faint">{fmtWhen(r.created_at)}</span>
              </div>
              <div className="mt-1 line-clamp-2 text-[13px] text-ink">{r.prompt || "—"}</div>
              {r.error ? (
                <div className="mt-1 line-clamp-2 text-[11px] text-accent">↳ {r.error}</div>
              ) : r.output ? (
                <div className="mt-1 line-clamp-2 text-[11px] text-muted">↳ {r.output}</div>
              ) : null}
              {/* Say what a partial run did NOT do. Without this the status is a
                  label the user has to go re-read the transcript to decode. */}
              {(r.budget_hit || (r.unfinished && r.unfinished.length > 0)) && (
                <div className="mt-1.5 rounded-lg border border-[#d2761f]/30 bg-[#d2761f]/[0.07] px-2 py-1.5 text-[11px] text-muted">
                  {r.budget_hit && <div>⚠ {BUDGET_REASON[r.budget_hit] || r.budget_hit},已停止并如实汇报</div>}
                  {r.unfinished && r.unfinished.length > 0 && (
                    <div className={r.budget_hit ? "mt-0.5" : ""}>
                      未完成 {r.unfinished.length} 步:{r.unfinished.slice(0, 3).join(" · ")}
                      {r.unfinished.length > 3 && ` 等 ${r.unfinished.length} 项`}
                    </div>
                  )}
                </div>
              )}
              <div className="mt-1.5 flex items-center gap-3 text-[11px] text-faint">
                <span>耗时 {fmtDur(r.duration_ms)}</span>
                {r.tool_calls > 0 && <span>工具 {r.tool_calls}</span>}
                {r.tokens > 0 && <span>{r.tokens >= 1000 ? (r.tokens / 1000).toFixed(1) + "k" : r.tokens} tok</span>}
                {r.result && <span title={r.result} className="text-ok">{"{ } 结构化"}</span>}
                {r.conversation_id && (
                  <button onClick={() => onJumpToConversation(r.conversation_id)} className="text-accent hover:underline">↗ 对话</button>
                )}
                {prevOf.has(r.run_id) && (
                  <button
                    onClick={() => setDiffOpen((id) => (id === r.run_id ? null : r.run_id))}
                    className={"hover:text-accent " + (diffOpen === r.run_id ? "text-accent" : "")}
                    title="与上一次同任务的运行对比"
                  >
                    ⇄ 对比上次
                  </button>
                )}
                {/* Continuing is offered BEFORE rerunning, and only when a
                    transcript survived: for a run that died deep in, redoing the
                    finished work is the expensive option, not the safe one. */}
                {r.resumable && (
                  <button
                    onClick={() =>
                      api.resumeRun(r.run_id)
                        .then((res) => {
                          toast(`已从第 ${r.resume_steps || res.steps || 0} 步继续`, "success");
                          if (res.conversation_id) onJumpToConversation(res.conversation_id);
                          setTimeout(() => refreshResource("runs:all"), 800);
                        })
                        .catch(() => toast("无法继续,可能记录已过期", "error"))
                    }
                    className="font-medium text-accent hover:underline"
                    title={`从中断处继续,保留已完成的 ${r.resume_steps || 0} 步`}
                  >
                    ▶ 继续
                  </button>
                )}
                <button
                  onClick={() => api.rerunRun(r.run_id).then(() => { toast("已重新触发", "success"); setTimeout(() => refreshResource("runs:all"), 800); }).catch(() => toast("重跑失败,请重试", "error"))}
                  className="hover:text-accent"
                >
                  ↻ 重跑
                </button>
              </div>
              {diffOpen === r.run_id && prevOf.get(r.run_id) && <RunDiff prev={prevOf.get(r.run_id)!} cur={r} />}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// FactorsPanel is the quant factor library — the output of the research-report
// → factor pipeline. Shows each ingested factor with its backtest scorecard,
// plus any weighted portfolios, and a button to run the pipeline over reports/.
const DIR_LABEL: Record<string, string> = { long: "多", short: "空", long_short: "多空" };

// AgreementTrend plots the double-blind consistency score of factors over time
// (chronological), so you can see whether extraction agreement is holding up
// across days/runs — a quality signal for the unattended pipeline.
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

function FactorsPanel() {
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

// RunDiff shows what changed between a scheduled run and the previous run of the
// same task — the core "what's different since last time" view for unattended
// automation. Diffs the answer text (falling back to the error when a run had
// no output), reusing the line-diff used by the file-version history.
function RunDiff({ prev, cur }: { prev: RunRecord; cur: RunRecord }) {
  const prevText = prev.output || prev.error || "";
  const curText = cur.output || cur.error || "";
  const rows = useMemo(() => lineDiff(prevText, curText), [prevText, curText]);
  const stats = diffStats(rows);
  return (
    <div className="mt-2 overflow-hidden rounded-lg border border-border bg-surface">
      <div className="flex items-center gap-2 border-b border-border px-2.5 py-1.5 text-[11px]">
        <span className="text-muted">{fmtWhen(prev.created_at)} → 本次</span>
        <span className="text-ok">+{stats.add}</span>
        <span className="text-accent">−{stats.del}</span>
      </div>
      <div className="max-h-56 overflow-auto px-1 py-1 font-mono text-[11.5px]">
        {prevText === curText ? (
          <div className="px-2 py-3 text-center text-[12px] text-muted">两次运行结果一致,没有变化。</div>
        ) : (
          rows.map((r, i) => (
            <div
              key={i}
              className={
                "whitespace-pre-wrap break-words px-2 " +
                (r.type === "add" ? "bg-ok/10 text-ok" : r.type === "del" ? "bg-accent/10 text-accent" : "text-muted")
              }
            >
              <span className="mr-2 select-none text-faint">{r.type === "add" ? "+" : r.type === "del" ? "−" : " "}</span>
              {r.text || " "}
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function TasksPanel({ onJumpToConversation }: { onJumpToConversation: (cid: string) => void }) {
  const [hookUrl, setHookUrl] = useState<Record<string, string>>({});
  const [creating, setCreating] = useState(false);
  const [prompt, setPrompt] = useState("");
  const [sec, setSec] = useState(3600);

  const tasks = useResource<TaskMeta[]>("tasks", () => api.getTasks().then((r) => r.tasks || []), { interval: 5000 }) ?? [];
  const refresh = () => refreshResource("tasks");

  const create = async () => {
    if (!prompt.trim()) return;
    try {
      await api.scheduleTask(prompt.trim(), sec, prompt.trim().slice(0, 24));
    } catch {
      toast("定时任务创建失败,请重试", "error");
      return;
    }
    setPrompt("");
    setCreating(false);
    refresh();
  };

  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[12px] text-faint">任务 · {tasks.length}</span>
        <button
          onClick={() => setCreating((o) => !o)}
          className="rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40"
        >
          {creating ? "取消" : "+ 定时任务"}
        </button>
      </div>

      {creating && (
        <div className="mb-3 space-y-2 rounded-xl border border-border bg-surface2/40 p-3">
          <textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            rows={2}
            placeholder="让 Orka 定时做什么?例如:总结今天的科技新闻"
            className="w-full resize-none rounded-lg border border-border bg-surface px-2.5 py-2 text-[13px] outline-none focus:border-accent/50"
          />
          <div className="flex items-center gap-2">
            <select
              value={sec}
              onChange={(e) => setSec(Number(e.target.value))}
              className="rounded-lg border border-border bg-surface px-2 py-1.5 text-[13px] outline-none"
            >
              {INTERVALS.map((i) => (
                <option key={i.sec} value={i.sec}>{i.label}</option>
              ))}
            </select>
            <button
              onClick={create}
              disabled={!prompt.trim()}
              className="ml-auto rounded-lg bg-accent px-3 py-1.5 text-[13px] text-white disabled:opacity-40"
            >
              创建
            </button>
          </div>
        </div>
      )}

      {tasks.length === 0 && (
        <Blank icon="clock" title="还没有定时任务" action={{ label: "+ 新建定时任务", onClick: () => setCreating(true) }}>
          把一句指令设成定时任务,Orka 会按周期自动跑(比如每天早上汇总昨日数据),也可以开 webhook 用外部事件触发。
        </Blank>
      )}
      <div className="space-y-1.5">
        {tasks.map((t) => {
          const scheduled = t.cron_status === "on";
          return (
            <div key={t.task_id} className="rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
              <div className="flex items-center gap-2">
                <span className={"text-[11px] font-medium " + (RUN_COLOR[t.run_status] || "text-muted")}>
                  ● {t.run_status}
                </span>
                {scheduled && (
                  <span className="rounded-full bg-accentsoft px-1.5 py-0.5 text-[10px] text-accent">
                    定时 · {INTERVALS.find((i) => i.sec === t.interval_sec)?.label || (t.interval_sec || 0) + "s"}
                  </span>
                )}
                <span className="ml-auto text-[10px] text-faint">{fmtWhen(t.created_at)}</span>
              </div>
              <div className="mt-1 line-clamp-2 text-[13px] text-ink">{taskPrompt(t)}</div>
              {scheduled && t.next_run_at ? (
                <div className="mt-1 text-[11px] text-faint">下次运行 {fmtWhen(t.next_run_at)}</div>
              ) : null}
              {t.last_result ? (
                <div className="mt-1 line-clamp-2 text-[11px] text-muted">↳ {t.last_result}</div>
              ) : null}
              <div className="mt-1.5 flex items-center gap-3">
                {t.conversation_id && (
                  <button
                    onClick={() => onJumpToConversation(t.conversation_id)}
                    className="text-[11px] text-accent hover:underline"
                  >
                    ↗ 打开对话
                  </button>
                )}
                {scheduled && (
                  <button
                    onClick={() => api.unscheduleTask(t.task_id).then(refresh).catch(() => toast("停用定时失败,请重试", "error"))}
                    className="text-[11px] text-faint hover:text-accent"
                  >
                    停用定时
                  </button>
                )}
                {t.webhook_token ? (
                  <button onClick={() => api.disableWebhook(t.task_id).then(refresh).catch(() => toast("关闭 webhook 失败,请重试", "error"))} className="text-[11px] text-faint hover:text-accent">关闭 webhook</button>
                ) : (
                  <button
                    onClick={() => api.enableWebhook(t.task_id).then((r) => { setHookUrl((m) => ({ ...m, [t.task_id]: location.origin + r.path })); refresh(); }).catch(() => toast("开启 webhook 失败,请重试", "error"))}
                    className="inline-flex items-center gap-1 text-[11px] text-faint hover:text-accent"
                  >
                    <Icon name="link" size={11} /> webhook
                  </button>
                )}
              </div>
              {(hookUrl[t.task_id] || t.webhook_token) && (
                <div className="mt-1 flex items-center gap-2 rounded-lg bg-surface2 px-2 py-1 text-[10px] text-muted">
                  <code className="min-w-0 flex-1 truncate">{hookUrl[t.task_id] || `${location.origin}/api/v1/controller/hook/${t.webhook_token}`}</code>
                  <button
                    onClick={() => navigator.clipboard?.writeText(hookUrl[t.task_id] || `${location.origin}/api/v1/controller/hook/${t.webhook_token}`)}
                    aria-label="复制 webhook URL" className="shrink-0 hover:text-accent"
                  >⧉</button>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// Blank is the panel empty state. A panel a user opens for the first time shows
// ONLY this, so it has to answer two questions: what appears here, and how do I
// make something appear. A bare "暂无 X" answers neither.
function Blank({
  icon, title, children, action,
}: {
  icon?: IconName;
  title?: string;
  children: React.ReactNode;
  action?: { label: string; onClick: () => void };
}) {
  // Legacy single-string usage still renders as a plain hint.
  if (!title) return <div className="p-6 text-center text-[13px] text-faint">{children}</div>;
  return (
    <div className="flex flex-col items-center px-6 py-10 text-center">
      {icon && (
        <div className="mb-3 grid h-11 w-11 place-items-center rounded-full bg-surface2 text-muted">
          <Icon name={icon} size={20} />
        </div>
      )}
      <div className="text-[14px] font-medium text-ink">{title}</div>
      <p className="mt-1.5 max-w-[34ch] text-[12.5px] leading-relaxed text-muted">{children}</p>
      {action && (
        <button
          onClick={action.onClick}
          className="mt-4 rounded-lg border border-border px-3 py-1.5 text-[12.5px] text-ink hover:border-accent/40 hover:bg-surface2"
        >
          {action.label}
        </button>
      )}
    </div>
  );
}

// fmtBytes renders a file size as a human-readable string (195 KB, not 200000b).
function fmtBytes(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1).replace(/\.0$/, "") + " KB";
  return (n / (1024 * 1024)).toFixed(1).replace(/\.0$/, "") + " MB";
}
