import { useEffect, useRef, useState } from "react";
import { api, auth, files as fileApi } from "../api";
import type { BrowserPayload, Message, MetricsSnapshot, TaskMeta } from "../types";
import { Markdown } from "./Markdown";

type Tab = "browser" | "files" | "metrics" | "tasks";

export function ArtifactDrawer({
  open,
  onClose,
  tab,
  setTab,
  messages,
  email,
}: {
  open: boolean;
  onClose: () => void;
  tab: Tab;
  setTab: (t: Tab) => void;
  messages: Message[];
  email: string;
}) {
  return (
    <aside
      className={
        "shrink-0 overflow-hidden border-l border-border bg-surface transition-all duration-300 " +
        (open ? "w-[400px]" : "w-0")
      }
    >
      <div className="flex h-full w-[400px] flex-col">
        <div className="flex items-center justify-between border-b border-border px-3 h-14">
          <div className="flex gap-1">
            {(["browser", "files", "metrics", "tasks"] as Tab[]).map((t) => (
              <button
                key={t}
                onClick={() => setTab(t)}
                className={
                  "rounded-lg px-2.5 py-1.5 text-[13px] capitalize transition " +
                  (tab === t ? "bg-accentsoft text-accent" : "text-muted hover:bg-surface2")
                }
              >
                {t}
              </button>
            ))}
          </div>
          <button
            onClick={onClose}
            className="grid h-8 w-8 place-items-center rounded-lg text-faint hover:bg-surface2"
            title="Close"
          >
            ✕
          </button>
        </div>
        <div className="flex-1 overflow-y-auto">
          {tab === "browser" && <BrowserPanel messages={messages} />}
          {tab === "files" && <FilesPanel email={email} />}
          {tab === "metrics" && <MetricsPanel />}
          {tab === "tasks" && <TasksPanel />}
        </div>
      </div>
    </aside>
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

function FilesPanel({ email }: { email: string }) {
  const [items, setItems] = useState<{ name: string; dir: boolean; size: number }[]>([]);
  const [pct, setPct] = useState<number | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
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
  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[12px] text-faint">/{email}</span>
        <button
          onClick={() => inputRef.current?.click()}
          className="rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40"
        >
          Upload
        </button>
        <input ref={inputRef} type="file" hidden onChange={(e) => e.target.files?.[0] && onUpload(e.target.files[0])} />
      </div>
      {pct !== null && (
        <div className="mb-2 h-1 w-full overflow-hidden rounded bg-surface2">
          <div className="h-full bg-accent transition-all" style={{ width: pct + "%" }} />
        </div>
      )}
      {items.length === 0 && <Blank>Empty</Blank>}
      <div className="space-y-0.5">
        {items.map((it) => (
          <div key={it.name} className="group flex items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface2">
            <span className="text-faint">{it.dir ? "📁" : "📄"}</span>
            {it.dir ? (
              <span className="flex-1 truncate text-[14px] text-ink">{it.name}</span>
            ) : (
              <button
                onClick={() => setPreview(it.name)}
                className="flex-1 truncate text-left text-[14px] text-ink hover:text-accent"
                title="预览"
              >
                {it.name}
              </button>
            )}
            <span className="text-[11px] text-faint">{it.size}b</span>
            {!it.dir && (
              <a href={fileApi.downloadURL(it.name)} className="text-[12px] text-accent opacity-0 group-hover:opacity-100">
                ↓
              </a>
            )}
            <button
              onClick={() => fileApi.delete(it.name).then(refresh)}
              className="text-[12px] text-faint opacity-0 hover:text-accent group-hover:opacity-100"
            >
              ✕
            </button>
          </div>
        ))}
      </div>
      {preview && <FilePreview name={preview} onClose={() => setPreview(null)} />}
    </div>
  );
}

// FilePreview renders a workspace file inline: images as <img>, everything else
// as text (markdown rendered, other text in a mono block).
function FilePreview({ name, onClose }: { name: string; onClose: () => void }) {
  const [content, setContent] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const isImage = /\.(png|jpe?g|gif|webp|svg)$/i.test(name);
  const isMd = /\.(md|markdown)$/i.test(name);
  const url = fileApi.downloadURL(name);

  useEffect(() => {
    if (isImage) return;
    fetch(url, { headers: { Authorization: "Bearer " + auth.token() } })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error("HTTP " + r.status))))
      .then((t) => setContent(t.slice(0, 100_000)))
      .catch((e) => setErr(String(e)));
  }, [url, isImage]);

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
        <div className="overflow-y-auto px-4 py-3">
          {isImage ? (
            <img src={url} alt={name} className="mx-auto max-w-full rounded" />
          ) : err ? (
            <div className="text-[13px] text-accent">无法预览:{err}</div>
          ) : content === null ? (
            <div className="text-[13px] text-faint">加载中…</div>
          ) : isMd ? (
            <Markdown>{content}</Markdown>
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

function TasksPanel() {
  const [tasks, setTasks] = useState<TaskMeta[]>([]);
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
              {scheduled && (
                <button
                  onClick={() => api.unscheduleTask(t.task_id).then(refresh)}
                  className="mt-1.5 text-[11px] text-faint hover:text-accent"
                >
                  停用定时
                </button>
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
