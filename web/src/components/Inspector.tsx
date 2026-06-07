import { useEffect, useRef, useState } from "react";
import { api, files as fileApi } from "../api";
import type { BrowserPayload, Message, MetricsSnapshot } from "../types";

type Tab = "browser" | "files" | "metrics";

export function Inspector({ messages, email }: { messages: Message[]; email: string }) {
  const [tab, setTab] = useState<Tab>("browser");
  return (
    <aside className="w-[380px] shrink-0 border-l hair bg-panel/40 flex flex-col">
      <div className="flex border-b hair">
        {(["browser", "files", "metrics"] as Tab[]).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={
              "flex-1 py-2.5 font-mono text-[10px] uppercase tracking-[0.18em] transition border-b-2 " +
              (tab === t
                ? "text-text border-live"
                : "text-faint border-transparent hover:text-muted")
            }
          >
            {t}
          </button>
        ))}
      </div>
      <div className="flex-1 overflow-y-auto">
        {tab === "browser" && <BrowserPanel messages={messages} />}
        {tab === "files" && <FilesPanel email={email} />}
        {tab === "metrics" && <MetricsPanel />}
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

  if (shots.length === 0) {
    return (
      <Blank>
        no browser viewport yet — enable <span className="text-browser">gui</span> and ask the
        agent to open a page
      </Blank>
    );
  }
  const cur = shots[Math.min(i, shots.length - 1)];
  return (
    <div className="p-3">
      <div className="rounded-lg border border-browser/30 overflow-hidden bg-ink">
        <div className="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-browser/20">
          <span className="h-2 w-2 rounded-full bg-danger/70" />
          <span className="h-2 w-2 rounded-full bg-tool/70" />
          <span className="h-2 w-2 rounded-full bg-ok/70" />
          <span className="ml-2 font-mono text-[10px] text-faint">
            viewport · frame {i + 1}/{shots.length}
          </span>
        </div>
        <img
          src={"data:image/png;base64," + cur.data}
          alt="browser viewport"
          className="w-full block"
        />
      </div>
      {shots.length > 1 && (
        <input
          type="range"
          min={0}
          max={shots.length - 1}
          value={i}
          onChange={(e) => setI(Number(e.target.value))}
          className="mt-3 w-full accent-[var(--color-browser)]"
        />
      )}
    </div>
  );
}

function FilesPanel({ email }: { email: string }) {
  const [path] = useState(".");
  const [items, setItems] = useState<{ name: string; dir: boolean; size: number }[]>([]);
  const [pct, setPct] = useState<number | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const refresh = () => fileApi.list(path, email).then(setItems).catch(() => setItems([]));
  useEffect(() => {
    refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
      <div className="flex items-center justify-between mb-2">
        <span className="font-mono text-[10px] text-faint">/{email}/{path === "." ? "" : path}</span>
        <button
          onClick={() => inputRef.current?.click()}
          className="font-mono text-[10px] uppercase rounded border border-ok/40 text-ok px-2 py-1 hover:bg-ok/10"
        >
          ↑ upload
        </button>
        <input
          ref={inputRef}
          type="file"
          hidden
          onChange={(e) => e.target.files?.[0] && onUpload(e.target.files[0])}
        />
      </div>
      {pct !== null && (
        <div className="mb-2 h-1 w-full rounded bg-line overflow-hidden">
          <div className="h-full bg-ok transition-all" style={{ width: pct + "%" }} />
        </div>
      )}
      <div className="space-y-0.5">
        {items.length === 0 && <Blank>empty</Blank>}
        {items.map((it) => (
          <div
            key={it.name}
            className="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-raised/40"
          >
            <span className="font-mono text-[12px]" style={{ color: it.dir ? "var(--color-browser)" : "var(--color-muted)" }}>
              {it.dir ? "▸" : "·"}
            </span>
            <span className="flex-1 truncate text-[13px] text-text">{it.name}</span>
            <span className="font-mono text-[10px] text-faint">{it.size}b</span>
            {!it.dir && (
              <a
                href={fileApi.downloadURL(it.name)}
                className="opacity-0 group-hover:opacity-100 font-mono text-[10px] text-ok hover:underline"
              >
                ↓
              </a>
            )}
            <button
              onClick={() => fileApi.delete(it.name, email).then(refresh)}
              className="opacity-0 group-hover:opacity-100 font-mono text-[10px] text-danger hover:underline"
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
    const tick = () => api.metrics().then(setM).catch(() => {});
    tick();
    const id = setInterval(tick, 2000);
    return () => clearInterval(id);
  }, []);
  const stats = [
    { k: "active sessions", v: m?.active_sessions ?? 0, c: "var(--color-live)" },
    { k: "checkpoints", v: m?.checkpoints ?? 0, c: "var(--color-clarify)" },
    { k: "tool calls", v: m?.tool_calls ?? 0, c: "var(--color-tool)" },
    { k: "avg tool µs", v: Math.round(m?.avg_tool_call_micros ?? 0), c: "var(--color-browser)" },
  ];
  return (
    <div className="p-3 grid grid-cols-2 gap-2.5">
      {stats.map((s) => (
        <div key={s.k} className="rounded-lg border hair bg-panel2/40 p-3">
          <div className="font-display text-[28px] font-bold leading-none" style={{ color: s.c }}>
            {s.v}
          </div>
          <div className="mt-1.5 font-mono text-[9px] uppercase tracking-[0.16em] text-faint">
            {s.k}
          </div>
        </div>
      ))}
    </div>
  );
}

function Blank({ children }: { children: React.ReactNode }) {
  return (
    <div className="p-6 text-center font-mono text-[11px] text-faint/60 leading-relaxed">
      {children}
    </div>
  );
}
