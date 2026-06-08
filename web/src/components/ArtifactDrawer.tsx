import { useEffect, useRef, useState } from "react";
import { api, files as fileApi } from "../api";
import type { BrowserPayload, Message, MetricsSnapshot, OwnerInfo, TaskMeta } from "../types";
import { initials } from "../lib/format";

type Tab = "browser" | "files" | "metrics" | "tasks";

export function ArtifactDrawer({
  open,
  onClose,
  tab,
  setTab,
  messages,
  email,
  tasks,
  owners,
}: {
  open: boolean;
  onClose: () => void;
  tab: Tab;
  setTab: (t: Tab) => void;
  messages: Message[];
  email: string;
  tasks: TaskMeta[];
  owners: Record<string, OwnerInfo>;
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
          {tab === "tasks" && <TasksPanel tasks={tasks} owners={owners} />}
        </div>
      </div>
    </aside>
  );
}

function BrowserPanel({ messages }: { messages: Message[] }) {
  const shots = messages
    .filter((m) => m.type === "browser" && (m.payload as BrowserPayload)?.data)
    .map((m) => m.payload as BrowserPayload);
  const [i, setI] = useState(0);
  useEffect(() => setI(Math.max(0, shots.length - 1)), [shots.length]);
  if (shots.length === 0) return <Blank>No browser viewport yet. Enable gui and open a page.</Blank>;
  const cur = shots[Math.min(i, shots.length - 1)];
  return (
    <div className="p-3">
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
    </div>
  );
}

function FilesPanel({ email }: { email: string }) {
  const [items, setItems] = useState<{ name: string; dir: boolean; size: number }[]>([]);
  const [pct, setPct] = useState<number | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const refresh = () => fileApi.list(".", email).then(setItems).catch(() => setItems([]));
  useEffect(() => {
    refresh(); /* eslint-disable-next-line */
  }, [email]);
  const onUpload = async (f: File) => {
    setPct(0);
    try {
      await fileApi.upload(f, "", email, setPct);
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
            <span className="flex-1 truncate text-[14px] text-ink">{it.name}</span>
            <span className="text-[11px] text-faint">{it.size}b</span>
            {!it.dir && (
              <a href={fileApi.downloadURL(it.name)} className="text-[12px] text-accent opacity-0 group-hover:opacity-100">
                ↓
              </a>
            )}
            <button
              onClick={() => fileApi.delete(it.name, email).then(refresh)}
              className="text-[12px] text-faint opacity-0 hover:text-accent group-hover:opacity-100"
            >
              ✕
            </button>
          </div>
        ))}
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
  const stats = [
    { k: "Active sessions", v: m?.active_sessions ?? 0 },
    { k: "Checkpoints", v: m?.checkpoints ?? 0 },
    { k: "Tool calls", v: m?.tool_calls ?? 0 },
    { k: "Avg tool µs", v: Math.round(m?.avg_tool_call_micros ?? 0) },
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

function TasksPanel({ tasks, owners }: { tasks: TaskMeta[]; owners: Record<string, OwnerInfo> }) {
  if (tasks.length === 0) return <Blank>No tasks</Blank>;
  return (
    <div className="space-y-1.5 p-3">
      {tasks.map((t) => {
        const o = owners[t.owner_email];
        return (
          <div key={t.task_id} className="flex items-center gap-2.5 rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
            <div className="grid h-8 w-8 place-items-center rounded-full bg-accent/90 text-[11px] text-white">
              {initials(o?.name || t.owner_email || "?")}
            </div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] text-ink">{o?.name || t.owner_email || "—"}</div>
              <div className="text-[11px] text-faint">{t.task_id.slice(0, 10)}</div>
            </div>
            <span className="text-[11px] text-muted">{t.run_status}</span>
          </div>
        );
      })}
    </div>
  );
}

function Blank({ children }: { children: React.ReactNode }) {
  return <div className="p-6 text-center text-[13px] text-faint">{children}</div>;
}
