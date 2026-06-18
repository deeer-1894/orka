import { useEffect, useRef, useState } from "react";
import { api, auth, files as fileApi } from "../api";
import type { BrowserPayload, Connector, Message, MetricsSnapshot, RunRecord, TaskMeta, ToolPayload, Workflow } from "../types";
import { toast } from "../lib/toast";
import { Markdown } from "./Markdown";

type Tab = "overview" | "computer" | "files" | "runs" | "tasks" | "flows" | "integrations" | "metrics";

const TAB_LABEL: Record<Tab, string> = {
  overview: "概览", runs: "运行", computer: "电脑", files: "文件", flows: "流程", tasks: "任务",
  integrations: "集成", metrics: "指标",
};

export function ArtifactDrawer({
  open,
  onClose,
  tab,
  setTab,
  computerView,
  setComputerView,
  messages,
  email,
  onJumpToConversation,
}: {
  open: boolean;
  onClose: () => void;
  tab: Tab;
  setTab: (t: Tab) => void;
  computerView: "terminal" | "browser";
  setComputerView: (v: "terminal" | "browser") => void;
  messages: Message[];
  email: string;
  onJumpToConversation: (cid: string) => void;
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
          <div className="flex flex-1 gap-0.5 overflow-x-auto no-scrollbar">
            {(["overview", "runs", "computer", "files", "flows", "tasks", "integrations", "metrics"] as Tab[]).map((t) => (
              <button
                key={t}
                onClick={() => setTab(t)}
                className={
                  "shrink-0 rounded-lg px-2 py-1.5 text-[12.5px] transition " +
                  (tab === t ? "bg-accentsoft text-accent" : "text-muted hover:bg-surface2")
                }
              >
                {TAB_LABEL[t]}
              </button>
            ))}
          </div>
          <button
            onClick={onClose}
            className="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-faint hover:bg-surface2"
            title="Close"
            aria-label="关闭工件面板"
          >
            ✕
          </button>
        </div>
        <div className="flex-1 overflow-y-auto">
          {tab === "overview" && <DashboardPanel onJumpToConversation={onJumpToConversation} />}
          {tab === "computer" && (
            <ComputerPanel messages={messages} view={computerView} setView={setComputerView} />
          )}
          {tab === "files" && <FilesPanel email={email} />}
          {tab === "runs" && <RunsPanel onJumpToConversation={onJumpToConversation} />}
          {tab === "flows" && <WorkflowsPanel onJumpToConversation={onJumpToConversation} />}
          {tab === "integrations" && <ConnectorsPanel />}
          {tab === "metrics" && <MetricsPanel />}
          {tab === "tasks" && <TasksPanel onJumpToConversation={onJumpToConversation} />}
        </div>
      </div>
    </aside>
  );
}

// DashboardPanel is the at-a-glance overview: it aggregates the run history and
// live metrics into headline stats, a recent-activity strip, and trigger mix —
// so the platform's activity is visible without digging through the run list.
function DashboardPanel({ onJumpToConversation }: { onJumpToConversation: (cid: string) => void }) {
  const [runs, setRuns] = useState<RunRecord[]>([]);
  const [metrics, setMetrics] = useState<MetricsSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    Promise.all([api.listRuns({}).catch(() => ({ runs: [] })), api.metrics().catch(() => null)])
      .then(([r, m]) => { setRuns(r.runs || []); setMetrics(m); })
      .finally(() => setLoading(false));
  }, []);

  if (loading) return <Blank>加载中…</Blank>;
  if (runs.length === 0) return <Blank>还没有运行记录。跑一个任务后这里会出现统计。</Blank>;

  const done = runs.filter((r) => r.status === "done").length;
  const failed = runs.filter((r) => r.status === "failed").length;
  const finished = done + failed;
  const successRate = finished ? Math.round((done / finished) * 100) : 0;
  const totalTokens = runs.reduce((a, r) => a + (r.tokens || 0), 0);
  const totalTools = runs.reduce((a, r) => a + (r.tool_calls || 0), 0);
  const durs = runs.filter((r) => r.duration_ms > 0).map((r) => r.duration_ms);
  const avgDur = durs.length ? Math.round(durs.reduce((a, b) => a + b, 0) / durs.length / 1000) : 0;
  const triggers = runs.reduce((acc, r) => { const k = r.trigger || "manual"; acc[k] = (acc[k] || 0) + 1; return acc; }, {} as Record<string, number>);
  const fmtNum = (n: number) => (n >= 1000 ? (n / 1000).toFixed(1).replace(/\.0$/, "") + "k" : String(n));
  const dot = (s: string) => (s === "done" ? "var(--color-ok)" : s === "failed" ? "#e0695f" : s === "running" ? "#e3b341" : "var(--color-faint)");
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
      <div className="grid grid-cols-2 gap-2">
        <Stat label="总运行" value={String(runs.length)} sub={`${done} 成功 · ${failed} 失败`} />
        <Stat label="成功率" value={successRate + "%"} sub={`${finished} 个已完成`} />
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
        <div className="mt-1.5 flex gap-3 text-[10.5px] text-faint">
          <span><span style={{ color: "var(--color-ok)" }}>●</span> 成功</span>
          <span><span style={{ color: "#e0695f" }}>●</span> 失败</span>
          <span><span style={{ color: "#e3b341" }}>●</span> 进行中</span>
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

// ComputerPanel is the Manus-style "watch it work" surface: one place for the
// agent's whole computer — a terminal (shell + files it wrote) and the browser
// (live view + captured frames) — switchable via sub-tabs. The browser is just
// another part of the computer, so it lives here rather than as a sibling tab.
function ComputerPanel({
  messages,
  view,
  setView,
}: {
  messages: Message[];
  view: "terminal" | "browser";
  setView: (v: "terminal" | "browser") => void;
}) {
  const shotCount = messages.filter(
    (m) => m.type === "browser" && (m.payload as BrowserPayload)?.data,
  ).length;
  return (
    <div className="flex h-full flex-col">
      <div className="flex gap-1 px-3 pt-2.5">
        {([
          ["terminal", "⌨️ 终端"],
          ["browser", `🌐 浏览器${shotCount ? ` (${shotCount})` : ""}`],
        ] as const).map(([id, label]) => (
          <button
            key={id}
            onClick={() => setView(id)}
            className={
              "rounded-lg px-2.5 py-1 text-[12.5px] transition " +
              (view === id ? "bg-accentsoft text-accent" : "text-muted hover:bg-surface2")
            }
          >
            {label}
          </button>
        ))}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {view === "terminal" ? <TerminalView messages={messages} /> : <BrowserPanel messages={messages} />}
      </div>
    </div>
  );
}

// TerminalView shows the agent's shell commands + output and the files it wrote.
function TerminalView({ messages }: { messages: Message[] }) {
  const endRef = useRef<HTMLDivElement>(null);
  const tools = messages
    .filter((m) => m.type === "tool")
    .map((m) => m.payload as ToolPayload)
    .filter(Boolean);
  const shellRuns = tools.filter((p) => p.tool === "shell");
  const files = [
    ...new Set(
      tools
        .filter((p) => p.tool === "file_write")
        .map((p) => String(p.args?.path ?? ""))
        .filter(Boolean),
    ),
  ];
  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [shellRuns.length]);

  if (shellRuns.length === 0 && files.length === 0) {
    return <Blank>还没有终端活动。让 Orka 运行脚本或命令后,这里会实时显示终端输出与生成的文件。</Blank>;
  }

  return (
    <div className="space-y-3 p-3">
      <div className="overflow-hidden rounded-xl border border-border">
        <div className="flex items-center gap-1.5 border-b border-border bg-surface2 px-3 py-2">
          <span className="h-2.5 w-2.5 rounded-full bg-[#e0695f]" />
          <span className="h-2.5 w-2.5 rounded-full bg-[#e3b341]" />
          <span className="h-2.5 w-2.5 rounded-full bg-[#5aa469]" />
          <span className="ml-2 text-[12px] text-faint">workspace · terminal</span>
        </div>
        <div className="max-h-[460px] overflow-y-auto bg-[#161513] px-3 py-2.5 font-mono text-[12px] leading-relaxed">
          {shellRuns.length === 0 ? (
            <div className="text-[#837d72]">尚无命令执行(仅写入了文件)</div>
          ) : (
            shellRuns.map((r, i) => {
              const cmd = String(r.args?.command ?? "");
              const out = (r.result ?? "").trim();
              return (
                <div key={i} className="mb-3">
                  <div className="flex gap-1.5">
                    <span className="shrink-0 text-[#6fa57d]">$</span>
                    <span className="whitespace-pre-wrap break-all text-[#ece9e2]">{cmd}</span>
                  </div>
                  {out && (
                    <pre className="mt-1 whitespace-pre-wrap break-all text-[#a8a399]">
                      {out.length > 1500 ? out.slice(0, 1500) + "\n…" : out}
                    </pre>
                  )}
                </div>
              );
            })
          )}
          <div ref={endRef} />
        </div>
      </div>

      {files.length > 0 && (
        <div className="rounded-xl border border-border p-2.5">
          <div className="mb-1.5 px-1 text-[11px] font-medium uppercase tracking-wide text-faint">
            工作区文件 · {files.length}
          </div>
          <div className="space-y-0.5">
            {files.map((f) => (
              <a
                key={f}
                href={fileApi.downloadURL(f)}
                target="_blank"
                rel="noreferrer"
                className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-[13px] text-ink hover:bg-surface2"
              >
                <span>📄</span>
                <span className="truncate">{f}</span>
                <span className="ml-auto text-[11px] text-faint">↓</span>
              </a>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function BrowserPanel({ messages }: { messages: Message[] }) {
  const novnc =
    (import.meta as unknown as { env?: Record<string, string> }).env?.VITE_NOVNC_URL ||
    "http://localhost:6080/vnc.html?autoconnect=1&resize=scale&reconnect=1";
  const shots = messages
    .filter((m) => m.type === "browser" && (m.payload as BrowserPayload)?.data)
    .map((m) => m.payload as BrowserPayload);
  const [i, setI] = useState(0);
  const [mode, setMode] = useState<"live" | "frames">("live");
  useEffect(() => setI(Math.max(0, shots.length - 1)), [shots.length]);

  return (
    <div className="p-3">
      <div className="mb-2 flex gap-1">
        {(["live", "frames"] as const).map((m) => (
          <button
            key={m}
            onClick={() => setMode(m)}
            className={
              "rounded-lg px-2.5 py-1 text-[12px] capitalize transition " +
              (mode === m ? "bg-accentsoft text-accent" : "text-muted hover:bg-surface2")
            }
          >
            {m === "frames" ? `frames (${shots.length})` : "live"}
          </button>
        ))}
      </div>

      {mode === "live" ? (
        <div className="overflow-hidden rounded-xl border border-border">
          <div className="flex items-center gap-1.5 border-b border-border bg-surface2 px-3 py-2">
            <span className="h-2.5 w-2.5 rounded-full bg-[#e0695f]" />
            <span className="h-2.5 w-2.5 rounded-full bg-[#e3b341]" />
            <span className="h-2.5 w-2.5 rounded-full bg-[#5aa469]" />
            <span className="ml-2 text-[12px] text-faint">sandbox · live</span>
          </div>
          <iframe
            src={novnc}
            title="live browser"
            className="block w-full bg-white"
            style={{ height: 520, border: 0 }}
          />
          <div className="border-t border-border px-3 py-1.5 text-[11px] text-faint">
            Live noVNC at {novnc}. Start the sandbox if blank: <code>make browser</code>
          </div>
        </div>
      ) : shots.length === 0 ? (
        <Blank>No frames yet. Enable gui and open a page.</Blank>
      ) : (
        <FramesView shots={shots} i={i} setI={setI} />
      )}
    </div>
  );
}

function FramesView({
  shots,
  i,
  setI,
}: {
  shots: BrowserPayload[];
  i: number;
  setI: (n: number) => void;
}) {
  const cur = shots[Math.min(i, shots.length - 1)];
  return (
    <>
      <div className="overflow-hidden rounded-xl border border-border">
        <div className="flex items-center gap-1.5 border-b border-border bg-surface2 px-3 py-2">
          <span className="h-2.5 w-2.5 rounded-full bg-[#e0695f]" />
          <span className="h-2.5 w-2.5 rounded-full bg-[#e3b341]" />
          <span className="h-2.5 w-2.5 rounded-full bg-[#5aa469]" />
          <span className="ml-2 text-[12px] text-faint">
            frame {i + 1}/{shots.length}
          </span>
        </div>
        <img src={"data:image/png;base64," + cur.data} alt="viewport" className="block w-full" />
      </div>
      {shots.length > 1 && (
        <input
          type="range"
          min={0}
          max={shots.length - 1}
          value={i}
          onChange={(e) => setI(Number(e.target.value))}
          className="mt-3 w-full accent-[var(--color-accent)]"
        />
      )}
    </>
  );
}

// File "kinds" group the flat workspace into tidy, labelled sections so a busy
// workspace reads as 文档 / 代码 / 数据 / 图片 / 其他 instead of one long dump.
const FILE_KINDS: { id: string; icon: string; label: string; exts?: string[] }[] = [
  { id: "folder", icon: "📁", label: "文件夹" },
  { id: "doc", icon: "📄", label: "文档 / 报告", exts: ["md", "markdown", "txt", "pdf", "doc", "docx", "rtf"] },
  { id: "code", icon: "💻", label: "代码", exts: ["py", "js", "jsx", "ts", "tsx", "go", "java", "c", "h", "cpp", "rs", "sh", "rb", "php", "html", "css", "yaml", "yml", "sql"] },
  { id: "data", icon: "📊", label: "数据", exts: ["json", "csv", "tsv", "xml", "ndjson"] },
  { id: "image", icon: "🖼️", label: "图片", exts: ["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp"] },
  { id: "other", icon: "🗂️", label: "其他" },
];
function kindOf(name: string, dir: boolean): string {
  if (dir) return "folder";
  const ext = name.split(".").pop()?.toLowerCase() || "";
  for (const k of FILE_KINDS) if (k.exts?.includes(ext)) return k.id;
  return "other";
}

type FileItem = { name: string; dir: boolean; size: number };

function FilesPanel({ email }: { email: string }) {
  const [items, setItems] = useState<FileItem[]>([]);
  const [pct, setPct] = useState<number | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const [query, setQuery] = useState("");
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
  const filtered = items
    .filter((it) => !q || it.name.toLowerCase().includes(q))
    .sort((a, b) => a.name.localeCompare(b.name));
  const grouped = FILE_KINDS.map((k) => ({ ...k, files: filtered.filter((it) => kindOf(it.name, it.dir) === k.id) })).filter(
    (g) => g.files.length > 0,
  );

  const Row = (it: FileItem) => (
    <div key={it.name} className="group flex items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface2">
      <span className="text-faint">{it.dir ? "📁" : "📄"}</span>
      {it.dir ? (
        <span className="flex-1 truncate text-[14px] text-ink">{it.name}</span>
      ) : (
        <button onClick={() => setPreview(it.name)} className="flex-1 truncate text-left text-[14px] text-ink hover:text-accent" title="预览">
          {it.name}
        </button>
      )}
      <span className="text-[11px] text-faint">{fmtBytes(it.size)}</span>
      {!it.dir && (
        <a href={fileApi.downloadURL(it.name)} className="text-[12px] text-accent opacity-0 group-hover:opacity-100" aria-label={"下载 " + it.name}>
          ↓
        </a>
      )}
      <button onClick={() => del(it.name)} className="text-[12px] text-faint opacity-0 hover:text-accent group-hover:opacity-100" aria-label={"删除 " + it.name}>
        ✕
      </button>
    </div>
  );

  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="truncate text-[12px] text-faint">/{email}</span>
        <button
          onClick={() => inputRef.current?.click()}
          className="shrink-0 rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40"
        >
          Upload
        </button>
        <input ref={inputRef} type="file" hidden onChange={(e) => e.target.files?.[0] && onUpload(e.target.files[0])} />
      </div>
      {items.length > 6 && (
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="筛选文件…"
          className="mb-2 w-full rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[12.5px] outline-none focus:border-accent/50"
        />
      )}
      {pct !== null && (
        <div className="mb-2 h-1 w-full overflow-hidden rounded bg-surface2">
          <div className="h-full bg-accent transition-all" style={{ width: pct + "%" }} />
        </div>
      )}
      {filtered.length === 0 && <Blank>{items.length === 0 ? "Empty" : "无匹配文件"}</Blank>}
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

// resolveWorkspaceImage maps a markdown image src to the workspace file API,
// relative to the directory of the .md file being previewed. Absolute/data/blob
// URLs are left untouched. Without this, ![](chart.png) loads from the page
// origin (localhost:5173/chart.png) and 404s.
function resolveWorkspaceImage(mdPath: string) {
  const dir = mdPath.includes("/") ? mdPath.slice(0, mdPath.lastIndexOf("/") + 1) : "";
  return (src: string) => {
    if (/^([a-z]+:|\/\/|#)/i.test(src)) return src; // http(s):, data:, blob:, protocol-relative
    const rel = (dir + src.replace(/^\.\//, "")).replace(/^\//, "");
    return fileApi.previewURL(rel);
  };
}

// FilePreview renders a workspace file inline by type: images as <img>, PDFs in
// an <iframe> (native browser viewer), text/markdown fetched and rendered, and
// any other binary (docx, xlsx, zip…) as a download card — never dumped as raw
// bytes, which is what produced the "乱码" for PDFs.
function FilePreview({ name, onClose }: { name: string; onClose: () => void }) {
  const [content, setContent] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const isImage = /\.(png|jpe?g|gif|webp|svg)$/i.test(name);
  const isPdf = /\.pdf$/i.test(name);
  const isMd = /\.(md|markdown)$/i.test(name);
  // Allowlist of extensions safe to show as text; anything else binary.
  const isText =
    isMd || /\.(txt|csv|tsv|json|ya?ml|xml|html?|css|js|ts|tsx|jsx|py|go|rs|java|c|cpp|h|sh|sql|toml|ini|conf|log|rtf)$/i.test(name);
  const url = fileApi.downloadURL(name);

  useEffect(() => {
    if (!isText) return;
    fetch(url, { headers: { Authorization: "Bearer " + auth.token() } })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error("HTTP " + r.status))))
      .then((t) => setContent(t.slice(0, 100_000)))
      .catch((e) => setErr(String(e)));
  }, [url, isText]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
      <div
        className="flex max-h-[80vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-border bg-surface shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
          <span className="text-faint">📄</span>
          <span className="flex-1 truncate text-[14px] text-ink">{name}</span>
          <a href={url} className="text-[12px] text-accent hover:underline">下载</a>
          <button onClick={onClose} className="ml-1 text-faint hover:text-ink">✕</button>
        </div>
        <div className={isPdf ? "overflow-hidden" : "overflow-y-auto px-4 py-3"}>
          {isImage ? (
            <img src={url} alt={name} className="mx-auto max-w-full rounded" />
          ) : isPdf ? (
            <iframe src={fileApi.previewURL(name)} title={name} className="h-[70vh] w-full border-0 bg-white" />
          ) : !isText ? (
            <div className="px-4 py-10 text-center">
              <div className="text-[34px]">📄</div>
              <div className="mt-2 text-[13px] text-muted">这是二进制文件,无法在此预览。</div>
              <a href={url} className="mt-3 inline-block rounded-lg bg-accentsoft px-3 py-1.5 text-[12.5px] text-accent hover:underline">
                下载 {name}
              </a>
            </div>
          ) : err ? (
            <div className="text-[13px] text-accent">无法预览:{err}</div>
          ) : content === null ? (
            <div className="text-[13px] text-faint">加载中…</div>
          ) : isMd ? (
            <Markdown resolveImage={resolveWorkspaceImage(name)}>{content}</Markdown>
          ) : (
            <pre className="whitespace-pre-wrap break-words font-mono text-[12.5px] text-ink">{content}</pre>
          )}
        </div>
      </div>
    </div>
  );
}

function MetricsPanel() {
  const [m, setM] = useState<MetricsSnapshot | null>(null);
  useEffect(() => {
    const t = () => api.metrics().then(setM).catch(() => {});
    t();
    const id = setInterval(t, 2000);
    return () => clearInterval(id);
  }, []);
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

// WorkflowsPanel: define + run multi-step pipelines. Each non-empty line of the
// editor is one step (a prompt); running it executes the steps as sequential
// turns in one conversation (chaining via conversation memory).
function WorkflowsPanel({ onJumpToConversation }: { onJumpToConversation: (cid: string) => void }) {
  const [flows, setFlows] = useState<Workflow[]>([]);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [stepsText, setStepsText] = useState("");
  const refresh = () => api.listWorkflows().then((r) => setFlows(r.workflows || [])).catch(() => {});
  useEffect(() => { refresh(); }, []);

  const save = async () => {
    const steps = stepsText.split("\n").map((l) => l.trim()).filter(Boolean).map((p, i) => ({ name: `步骤 ${i + 1}`, prompt: p }));
    if (!name.trim() || steps.length === 0) return;
    await api.createWorkflow(name.trim(), steps).catch(() => {});
    setName(""); setStepsText(""); setAdding(false); refresh();
  };
  const run = async (id: string) => {
    const r = await api.runWorkflow(id).catch(() => null);
    if (r?.conversation_id) { toast("流程已启动", "success"); onJumpToConversation(r.conversation_id); }
  };

  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[12px] text-faint">工作流 · {flows.length}</span>
        <button onClick={() => setAdding((o) => !o)} className="rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40">
          {adding ? "取消" : "+ 新建流程"}
        </button>
      </div>
      {adding && (
        <div className="mb-3 space-y-2 rounded-xl border border-border bg-surface2/40 p-3">
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="流程名称，如 每日竞品简报" className="w-full rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] outline-none focus:border-accent/50" />
          <textarea value={stepsText} onChange={(e) => setStepsText(e.target.value)} rows={4} placeholder="每行一个步骤（prompt），按顺序执行，后一步可引用前一步结果。例如：&#10;调研三家竞品最新定价&#10;把结果整理成表格&#10;写一段对比结论并存为 compare.md" className="w-full resize-none rounded-lg border border-border bg-surface px-2.5 py-2 text-[13px] outline-none focus:border-accent/50" />
          <button onClick={save} disabled={!name.trim() || !stepsText.trim()} className="w-full rounded-lg bg-accent px-3 py-1.5 text-[13px] text-white disabled:opacity-40">保存流程</button>
        </div>
      )}
      {flows.length === 0 && !adding && <Blank>暂无工作流。把一件多步骤的事拆成几步,Orka 按序执行。</Blank>}
      <div className="space-y-1.5">
        {flows.map((wf) => (
          <div key={wf.workflow_id} className="rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
            <div className="flex items-center gap-2">
              <span className="text-[15px]">🧩</span>
              <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{wf.name}</span>
              <span className="text-[11px] text-faint">{wf.steps.length} 步</span>
            </div>
            <ol className="mt-1 ml-5 list-decimal text-[11px] text-muted">
              {wf.steps.slice(0, 4).map((s, i) => <li key={i} className="truncate">{s.prompt}</li>)}
            </ol>
            <div className="mt-1.5 flex items-center gap-3 text-[11px]">
              <button onClick={() => run(wf.workflow_id)} className="text-accent hover:underline">▶ 运行</button>
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

      {conns.length === 0 && !adding && <Blank>未连接任何外部工具。添加一个 MCP 服务,Orka 即可调用它的工具。</Blank>}
      <div className="space-y-1.5">
        {conns.map((cn) => (
          <div key={cn.connector_id} className="flex items-center gap-2 rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
            <span className="text-[15px]">🔌</span>
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] text-ink">{cn.name}</div>
              <div className="truncate text-[11px] text-faint">{cn.transport} · {cn.url}</div>
            </div>
            <button onClick={() => api.deleteConnector(cn.connector_id).then(refresh)} aria-label="移除集成" className="text-[12px] text-faint hover:text-accent">✕</button>
          </div>
        ))}
      </div>
    </div>
  );
}

const RUN_STATUS: Record<string, { label: string; cls: string }> = {
  running: { label: "运行中", cls: "text-accent" },
  done: { label: "完成", cls: "text-ok" },
  failed: { label: "失败", cls: "text-accent" },
  paused: { label: "等待澄清", cls: "text-muted" },
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
  const [runs, setRuns] = useState<RunRecord[]>([]);
  const [onlyFailed, setOnlyFailed] = useState(false);
  const refresh = () =>
    api.listRuns(onlyFailed ? { status: "failed" } : {}).then((r) => setRuns(r.runs || [])).catch(() => {});
  useEffect(() => {
    refresh();
    const id = setInterval(refresh, 5000);
    return () => clearInterval(id); /* eslint-disable-next-line */
  }, [onlyFailed]);

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
      {runs.length === 0 && <Blank>暂无运行记录</Blank>}
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
              <div className="mt-1.5 flex items-center gap-3 text-[11px] text-faint">
                <span>耗时 {fmtDur(r.duration_ms)}</span>
                {r.tool_calls > 0 && <span>工具 {r.tool_calls}</span>}
                {r.tokens > 0 && <span>{r.tokens >= 1000 ? (r.tokens / 1000).toFixed(1) + "k" : r.tokens} tok</span>}
                {r.result && <span title={r.result} className="text-ok">{"{ } 结构化"}</span>}
                {r.conversation_id && (
                  <button onClick={() => onJumpToConversation(r.conversation_id)} className="text-accent hover:underline">↗ 对话</button>
                )}
                <button
                  onClick={() => api.rerunRun(r.run_id).then(() => { toast("已重新触发", "success"); setTimeout(refresh, 800); }).catch(() => {})}
                  className="hover:text-accent"
                >
                  ↻ 重跑
                </button>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function TasksPanel({ onJumpToConversation }: { onJumpToConversation: (cid: string) => void }) {
  const [tasks, setTasks] = useState<TaskMeta[]>([]);
  const [hookUrl, setHookUrl] = useState<Record<string, string>>({});
  const [creating, setCreating] = useState(false);
  const [prompt, setPrompt] = useState("");
  const [sec, setSec] = useState(3600);

  const refresh = () => api.getTasks().then((r) => setTasks(r.tasks || [])).catch(() => {});
  useEffect(() => {
    refresh();
    const id = setInterval(refresh, 5000);
    return () => clearInterval(id);
  }, []);

  const create = async () => {
    if (!prompt.trim()) return;
    await api.scheduleTask(prompt.trim(), sec, prompt.trim().slice(0, 24));
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

      {tasks.length === 0 && <Blank>暂无任务</Blank>}
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
                    onClick={() => api.unscheduleTask(t.task_id).then(refresh)}
                    className="text-[11px] text-faint hover:text-accent"
                  >
                    停用定时
                  </button>
                )}
                {t.webhook_token ? (
                  <button onClick={() => api.disableWebhook(t.task_id).then(refresh)} className="text-[11px] text-faint hover:text-accent">关闭 webhook</button>
                ) : (
                  <button
                    onClick={() => api.enableWebhook(t.task_id).then((r) => { setHookUrl((m) => ({ ...m, [t.task_id]: location.origin + r.path })); refresh(); }).catch(() => {})}
                    className="text-[11px] text-faint hover:text-accent"
                  >
                    🪝 webhook
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

function Blank({ children }: { children: React.ReactNode }) {
  return <div className="p-6 text-center text-[13px] text-faint">{children}</div>;
}

// fmtBytes renders a file size as a human-readable string (195 KB, not 200000b).
function fmtBytes(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1).replace(/\.0$/, "") + " KB";
  return (n / (1024 * 1024)).toFixed(1).replace(/\.0$/, "") + " MB";
}
