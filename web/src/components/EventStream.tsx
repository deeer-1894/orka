import { useEffect, useRef, useState } from "react";
import type { RunStatus } from "../hooks/useChatStream";
import type { BrowserPayload, ClarifyPayload, Message, ToolPayload } from "../types";
import { fmtTime, shortTrace } from "../lib/format";

const COLOR: Record<string, string> = {
  chat: "var(--color-muted)",
  tool: "var(--color-tool)",
  browser: "var(--color-browser)",
  clarify: "var(--color-clarify)",
  task: "var(--color-live)",
  agent: "var(--color-live)",
  skill: "var(--color-clarify)",
  file: "var(--color-ok)",
};

export function EventStream({
  messages,
  status,
  onResume,
}: {
  messages: Message[];
  status: RunStatus;
  onResume: (resumeKey: string, answer: string) => void;
}) {
  const endRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages.length]);

  return (
    <div className="flex-1 overflow-y-auto px-6 py-5">
      {messages.length === 0 && <Empty />}
      <div className="mx-auto max-w-3xl">
        {messages.map((m) => (
          <Row key={m.id} m={m} onResume={onResume} />
        ))}
        {status === "streaming" && <Baseline />}
        <div ref={endRef} />
      </div>
    </div>
  );
}

function Row({ m, onResume }: { m: Message; onResume: (k: string, a: string) => void }) {
  const color = COLOR[m.type] ?? "var(--color-muted)";
  return (
    <div className="feed-in group grid grid-cols-[120px_1fr] gap-4 py-2.5">
      <div className="flex items-start gap-2 pt-1 select-none">
        <span className="mt-1 h-2 w-2 shrink-0 rounded-[2px]" style={{ background: color }} />
        <div className="flex flex-col">
          <span className="font-mono text-[10px] text-faint leading-tight">{fmtTime(m.ts)}</span>
          <span className="font-mono text-[9px] text-faint/70 leading-tight">
            {shortTrace(m.meta?.trace_id)}
          </span>
        </div>
      </div>
      <div className="min-w-0">
        <Body m={m} color={color} onResume={onResume} />
      </div>
    </div>
  );
}

function Chip({ color, children }: { color: string; children: React.ReactNode }) {
  return (
    <span
      className="font-mono text-[9px] uppercase tracking-[0.18em] px-1.5 py-0.5 rounded border"
      style={{ color, borderColor: color, background: "rgba(255,255,255,0.04)" }}
    >
      {children}
    </span>
  );
}

function Body({
  m,
  color,
  onResume,
}: {
  m: Message;
  color: string;
  onResume: (k: string, a: string) => void;
}) {
  switch (m.type) {
    case "chat":
      return <ChatBody m={m} />;
    case "tool":
      return <ToolBody p={m.payload as ToolPayload} color={color} />;
    case "browser":
      return <BrowserBody p={m.payload as BrowserPayload} color={color} />;
    case "clarify":
      return <ClarifyBody p={m.payload as ClarifyPayload} onResume={onResume} />;
    case "task":
      return <TaskBody action={m.action} content={m.content} />;
    default:
      return (
        <div className="text-[13px] text-muted">
          <Chip color={color}>{m.type}</Chip>{" "}
          {m.action && <span className="font-mono text-[11px] text-faint">{m.action}</span>}{" "}
          {m.content}
        </div>
      );
  }
}

function ChatBody({ m }: { m: Message }) {
  const isUser = m.role === "user";
  return (
    <div
      className={
        "rounded-lg border px-3.5 py-2.5 text-[14px] leading-relaxed whitespace-pre-wrap " +
        (isUser
          ? "border-line bg-raised/60 text-text"
          : "border-live/20 bg-live/[0.04] text-text")
      }
    >
      <div className="mb-1 flex items-center gap-2">
        <span
          className="font-mono text-[9px] uppercase tracking-[0.2em]"
          style={{ color: isUser ? "var(--color-muted)" : "var(--color-live)" }}
        >
          {isUser ? "user" : "assistant"}
        </span>
      </div>
      {m.content}
    </div>
  );
}

function ToolBody({ p, color }: { p: ToolPayload; color: string }) {
  return (
    <div className="rounded-lg border hair bg-panel2/50 overflow-hidden">
      <div className="flex items-center gap-2 px-3 py-1.5 border-b hair">
        <Chip color={color}>tool</Chip>
        <span className="font-mono text-[12px] text-tool font-medium">{p.tool}</span>
        {p.args && (
          <span className="font-mono text-[10px] text-faint truncate">
            {JSON.stringify(p.args)}
          </span>
        )}
      </div>
      <div className="px-3 py-1.5 font-mono text-[11px] whitespace-pre-wrap break-words">
        {p.error ? (
          <span className="text-danger">⨯ {p.error}</span>
        ) : (
          <span className="text-muted">{p.result}</span>
        )}
      </div>
    </div>
  );
}

function BrowserBody({ p, color }: { p: BrowserPayload; color: string }) {
  const kind = p.type ?? p.action ?? "event";
  return (
    <div className="flex items-center gap-2 text-[12px]">
      <Chip color={color}>browser</Chip>
      <span className="font-mono text-browser">{kind}</span>
      {p.mode && (
        <span
          className="font-mono text-[10px] px-1.5 rounded"
          style={{
            color: p.mode === "macro" ? "var(--color-live)" : "var(--color-browser)",
            background: "rgba(79,214,224,0.1)",
          }}
        >
          {p.mode} · {p.tokens ?? 0} vis-tok
        </span>
      )}
      {p.target && <span className="font-mono text-[11px] text-faint truncate">{p.target}</span>}
      {p.result && <span className="text-faint truncate">→ {p.result}</span>}
    </div>
  );
}

function ClarifyBody({
  p,
  onResume,
}: {
  p: ClarifyPayload;
  onResume: (k: string, a: string) => void;
}) {
  const [text, setText] = useState("");
  const [sent, setSent] = useState(false);
  const send = (a: string) => {
    if (!a.trim() || sent) return;
    setSent(true);
    onResume(p.resume_key, a.trim());
  };
  return (
    <div className="rounded-lg border border-clarify/30 bg-clarify/[0.05] px-4 py-3">
      <div className="mb-2 flex items-center gap-2">
        <Chip color="var(--color-clarify)">clarify</Chip>
        <span className="font-mono text-[10px] text-faint">checkpoint {p.resume_key.slice(0, 10)}</span>
      </div>
      <p className="text-[14px] text-text mb-3">{p.question}</p>
      <div className="flex flex-wrap gap-2">
        {(p.options ?? []).map((o) => (
          <button
            key={o}
            disabled={sent}
            onClick={() => send(o)}
            className="rounded-md border border-clarify/40 bg-clarify/10 px-3 py-1.5 text-[13px] text-text hover:bg-clarify/20 disabled:opacity-40 transition"
          >
            {o}
          </button>
        ))}
      </div>
      <div className="mt-2 flex gap-2">
        <input
          value={text}
          disabled={sent}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && send(text)}
          placeholder="or type a reply…"
          className="flex-1 rounded-md border hair bg-panel px-3 py-1.5 text-[13px] outline-none focus:border-clarify/50 disabled:opacity-40"
        />
      </div>
    </div>
  );
}

function TaskBody({ action, content }: { action?: string; content?: string }) {
  const tone =
    action === "failed"
      ? "var(--color-danger)"
      : action === "done"
      ? "var(--color-ok)"
      : "var(--color-live)";
  return (
    <div className="flex items-center gap-2 text-[12px]">
      <span className="h-px w-6" style={{ background: tone }} />
      <span className="font-mono text-[10px] uppercase tracking-[0.2em]" style={{ color: tone }}>
        task · {action}
      </span>
      {content && <span className="text-faint">{content}</span>}
    </div>
  );
}

function Baseline() {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-4 py-3">
      <span className="font-mono text-[10px] text-faint pt-1 pl-4">live</span>
      <div className="flex items-center">
        <span className="baseline-bar h-px w-40 bg-live" />
      </div>
    </div>
  );
}

function Empty() {
  return (
    <div className="h-full grid place-items-center text-center">
      <div>
        <div className="font-display text-[28px] font-bold text-faint/60">no telemetry yet</div>
        <p className="mt-2 font-mono text-[12px] text-faint/50 max-w-sm mx-auto">
          send a message below. tool calls, browser events, clarify interrupts and task
          state will stream here as a live feed.
        </p>
      </div>
    </div>
  );
}
