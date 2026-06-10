import { useEffect, useRef, useState } from "react";
import type { RunStatus } from "../hooks/useChatStream";
import type { BrowserPayload, ClarifyPayload, Message, ToolPayload, WeatherCardData } from "../types";
import { Markdown } from "./Markdown";
import { WeatherCard, parseWeatherCard } from "./WeatherCard";

type Block =
  | { kind: "user"; m: Message }
  | { kind: "assistant"; m: Message }
  | { kind: "clarify"; m: Message }
  | { kind: "weather"; data: WeatherCardData }
  | { kind: "steps"; items: Message[] };

function group(messages: Message[]): Block[] {
  const blocks: Block[] = [];
  let buf: Message[] = [];
  const flush = () => {
    if (buf.length) {
      blocks.push({ kind: "steps", items: buf });
      buf = [];
    }
  };
  for (const m of messages) {
    if (m.type === "task" || m.type === "heartbeat") continue;
    if (m.type === "chat") {
      flush();
      blocks.push({ kind: m.role === "user" ? "user" : "assistant", m });
    } else if (m.type === "clarify") {
      flush();
      blocks.push({ kind: "clarify", m });
    } else {
      // A weather tool result carries a structured card → surface it as its own
      // rich block (and still keep the tool step in the collapsible list).
      if (m.type === "tool" && (m.payload as ToolPayload)?.tool === "weather") {
        const card = parseWeatherCard((m.payload as ToolPayload)?.result);
        if (card) {
          flush();
          blocks.push({ kind: "weather", data: card });
        }
      }
      buf.push(m); // tool / browser / agent / skill / file
    }
  }
  flush();
  return blocks;
}

export function Thread({
  messages,
  status,
  onResume,
  onOpenViewport,
}: {
  messages: Message[];
  status: RunStatus;
  onResume: (key: string, answer: string) => void;
  onOpenViewport: () => void;
}) {
  const endRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages.length, status]);

  const blocks = group(messages);
  const thinking =
    status === "streaming" &&
    (blocks.length === 0 || blocks[blocks.length - 1].kind !== "assistant");

  if (messages.length === 0) return <Empty />;

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-3xl px-5 py-8">
        {blocks.map((b, i) => (
          <div key={i} className="rise">
            {b.kind === "user" && <UserBubble m={b.m} />}
            {b.kind === "assistant" && <Assistant m={b.m} />}
            {b.kind === "clarify" && <Clarify m={b.m} onResume={onResume} />}
            {b.kind === "weather" && <WeatherCard data={b.data} />}
            {b.kind === "steps" && <Steps items={b.items} onOpenViewport={onOpenViewport} />}
          </div>
        ))}
        {thinking && <Thinking />}
        <div ref={endRef} className="h-2" />
      </div>
    </div>
  );
}

function UserBubble({ m }: { m: Message }) {
  return (
    <div className="mb-6 flex justify-end">
      <div className="max-w-[85%] rounded-2xl rounded-br-md bg-userbubble px-4 py-2.5 text-[15px] leading-relaxed text-ink whitespace-pre-wrap">
        {m.content}
      </div>
    </div>
  );
}

function Assistant({ m }: { m: Message }) {
  return (
    <div className="mb-7 flex gap-3.5">
      <div className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-full bg-accent text-white font-serif text-[13px] leading-none">
        C
      </div>
      <div className="min-w-0 flex-1 pt-0.5">
        <Markdown>{m.content ?? ""}</Markdown>
      </div>
    </div>
  );
}

function Steps({ items, onOpenViewport }: { items: Message[]; onOpenViewport: () => void }) {
  const [open, setOpen] = useState(false);
  const hasShot = items.some((m) => m.type === "browser" && (m.payload as BrowserPayload)?.data);
  return (
    <div className="mb-6 ml-[42px]">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-2 rounded-full border border-border bg-surface px-3 py-1.5 text-[13px] text-muted hover:border-accent/40 transition"
      >
        <Spinner />
        <span>
          {open ? "Hide" : "Show"} {items.length} step{items.length > 1 ? "s" : ""}
        </span>
        {hasShot && (
          <span
            onClick={(e) => {
              e.stopPropagation();
              onOpenViewport();
            }}
            className="ml-1 text-accent hover:underline"
          >
            · view browser
          </span>
        )}
      </button>
      {open && (
        <div className="mt-2 space-y-1.5 border-l-2 border-border pl-4">
          {items.map((m) => (
            <Step key={m.id} m={m} />
          ))}
        </div>
      )}
    </div>
  );
}

function Step({ m }: { m: Message }) {
  if (m.type === "tool") {
    const p = (m.payload as ToolPayload) || ({} as ToolPayload);
    return (
      <div className="text-[13px]">
        <span className="font-mono text-ink">🔧 {p.tool || m.action || "tool"}</span>
        {p.error ? (
          <span className="text-accent"> · {p.error}</span>
        ) : p.result ? (
          <span className="text-muted"> · {trunc(stripCard(p.result), 90)}</span>
        ) : null}
      </div>
    );
  }
  if (m.type === "browser") {
    const p = m.payload as BrowserPayload;
    const kind = p?.type ?? p?.action ?? "event";
    return (
      <div className="text-[13px] text-muted">
        🌐 <span className="font-mono">{kind}</span>
        {p?.mode && <span className="text-faint"> ({p.mode})</span>}
        {p?.target && <span className="text-faint"> {trunc(p.target, 60)}</span>}
      </div>
    );
  }
  return (
    <div className="text-[13px] text-muted">
      <span className="font-mono">{m.type}</span> {m.action} {trunc(m.content, 80)}
    </div>
  );
}

function Clarify({ m, onResume }: { m: Message; onResume: (k: string, a: string) => void }) {
  const p = m.payload as ClarifyPayload;
  const [text, setText] = useState("");
  const [sent, setSent] = useState(false);
  const send = (a: string) => {
    if (!a.trim() || sent) return;
    setSent(true);
    onResume(p.resume_key, a.trim());
  };
  return (
    <div className="mb-7 ml-[42px] rounded-2xl border border-accent/30 bg-accentsoft/50 px-4 py-3.5">
      <div className="mb-2 text-[12px] font-medium uppercase tracking-wide text-accent">
        Needs your input
      </div>
      <p className="mb-3 text-[15px] text-ink">{p.question}</p>
      <div className="flex flex-wrap gap-2">
        {(p.options ?? []).map((o) => (
          <button
            key={o}
            disabled={sent}
            onClick={() => send(o)}
            className="rounded-lg border border-accent/40 bg-surface px-3 py-1.5 text-[14px] text-ink hover:bg-accentsoft disabled:opacity-40 transition"
          >
            {o}
          </button>
        ))}
      </div>
      <input
        value={text}
        disabled={sent}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && send(text)}
        placeholder="or type a reply…"
        className="mt-2.5 w-full rounded-lg border border-border bg-surface px-3 py-2 text-[14px] outline-none focus:border-accent/50 disabled:opacity-40"
      />
    </div>
  );
}

function Thinking() {
  return (
    <div className="mb-7 flex items-center gap-3.5">
      <div className="grid h-7 w-7 shrink-0 place-items-center rounded-full bg-accent text-white font-serif text-[13px]">
        C
      </div>
      <div className="flex gap-1">
        <span className="dot h-1.5 w-1.5 rounded-full bg-faint" />
        <span className="dot h-1.5 w-1.5 rounded-full bg-faint" />
        <span className="dot h-1.5 w-1.5 rounded-full bg-faint" />
      </div>
    </div>
  );
}

function Spinner() {
  return <span className="h-1.5 w-1.5 rounded-full bg-ok" />;
}

function Empty() {
  return (
    <div className="flex h-full items-center justify-center px-6">
      <div className="text-center">
        <h1 className="font-serif text-[34px] text-ink">How can I help today?</h1>
        <p className="mt-3 text-[15px] text-muted max-w-md mx-auto">
          问我任何问题。Orka 会按需自动调用搜索、网页、天气、文件读写或浏览器自动化 —— 工具步骤和截图会内联显示。
        </p>
      </div>
    </div>
  );
}

function trunc(s: string | undefined, n: number) {
  if (!s) return "";
  return s.length > n ? s.slice(0, n) + "…" : s;
}

// stripCard removes the embedded <weather-card> JSON block from a tool result
// so the collapsible step list shows only the human-readable text.
function stripCard(s: string): string {
  return s.replace(/\n?<weather-card>[\s\S]*?<\/weather-card>/g, "").trim();
}
