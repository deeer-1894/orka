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
    if (m.type === "chat" || m.type === "stream") {
      // "stream" is the live, transient assistant bubble (token deltas).
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
  onPick,
}: {
  messages: Message[];
  status: RunStatus;
  onResume: (key: string, answer: string) => void;
  onOpenViewport: () => void;
  onPick: (text: string) => void;
}) {
  const endRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages.length, status]);

  const blocks = group(messages);
  const thinking =
    status === "streaming" &&
    (blocks.length === 0 || blocks[blocks.length - 1].kind !== "assistant");

  if (messages.length === 0) return <Empty onPick={onPick} />;

  // Each user turn is a navigable anchor for the floating outline (TOC).
  const turns = blocks
    .filter((b): b is Extract<Block, { kind: "user" }> => b.kind === "user")
    .map((b) => ({ id: "turn-" + b.m.id, text: b.m.content || "" }));

  return (
    <div className="relative flex-1 overflow-y-auto">
      <ThreadOutline turns={turns} />
      <div className="mx-auto max-w-3xl px-5 py-8">
        {blocks.map((b, i) => (
          <div key={i} id={b.kind === "user" ? "turn-" + b.m.id : undefined} className="rise scroll-mt-4">
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

// ThreadOutline is a floating table-of-contents: it lists each user turn and
// scrolls to it on click — handy for navigating long conversations.
function ThreadOutline({ turns }: { turns: { id: string; text: string }[] }) {
  const [open, setOpen] = useState(false);
  if (turns.length < 2) return null;
  const go = (id: string) => {
    document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
    setOpen(false);
  };
  return (
    <div className="sticky top-0 z-10 float-right mr-2 mt-2">
      <button
        onClick={() => setOpen((o) => !o)}
        title="Outline"
        className={
          "grid h-8 w-8 place-items-center rounded-lg border text-muted transition " +
          (open ? "border-accent/40 bg-accentsoft text-accent" : "border-border bg-surface hover:bg-surface2")
        }
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
          <path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        </svg>
      </button>
      {open && (
        <div className="absolute right-0 mt-1.5 max-h-[60vh] w-64 overflow-y-auto rounded-xl border border-border bg-surface p-1.5 shadow-lg">
          <div className="px-2 py-1 text-[11px] font-medium uppercase tracking-wide text-faint">
            Outline · {turns.length} turns
          </div>
          {turns.map((t, i) => (
            <button
              key={t.id}
              onClick={() => go(t.id)}
              className="flex w-full items-start gap-2 rounded-lg px-2 py-1.5 text-left text-[13px] text-muted hover:bg-surface2 hover:text-ink"
            >
              <span className="mt-0.5 shrink-0 text-faint">{i + 1}.</span>
              <span className="line-clamp-2">{trunc(t.text, 60)}</span>
            </button>
          ))}
        </div>
      )}
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
        O
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

// toolReceipt turns a tool payload into a readable {icon,label,detail} receipt,
// so a step reads like "📄 wrote report.md · 1,785 bytes" instead of raw JSON.
function toolReceipt(p: ToolPayload): { icon: string; label: string; detail: string } {
  const a = (p.args || {}) as Record<string, unknown>;
  const s = (k: string) => (a[k] == null ? "" : String(a[k]));
  const res = stripCard(p.result || "");
  switch (p.tool) {
    case "file_write":
      return { icon: "📄", label: `写入 ${s("path")}`, detail: res };
    case "file_read":
      return { icon: "📄", label: `读取 ${s("path")}`, detail: trunc(res, 70) };
    case "file_list":
      return { icon: "📁", label: `列目录 ${s("path") || "/"}`, detail: trunc(res, 70) };
    case "web_search":
      return { icon: "🔎", label: `搜索 “${s("query")}”`, detail: trunc(res, 70) };
    case "fetch_url":
      return { icon: "🔗", label: `读取网页`, detail: trunc(s("url") || res, 70) };
    case "weather":
      return { icon: "🌤️", label: `天气 ${s("location")}`, detail: "" };
    case "current_time":
      return { icon: "🕐", label: "当前时间", detail: trunc(res, 60) };
    case "calculator":
      return { icon: "🧮", label: "计算", detail: trunc(res, 60) };
    case "unit_convert":
      return { icon: "📐", label: "单位换算", detail: trunc(res, 60) };
    case "http_request":
      return { icon: "🌍", label: `HTTP ${s("method") || "GET"}`, detail: trunc(s("url"), 60) };
    case "apply_skill":
      return { icon: "✨", label: `采纳技能 ${s("name")}`, detail: "" };
    default:
      return { icon: "🔧", label: p.tool || "tool", detail: trunc(res, 70) };
  }
}

function Step({ m }: { m: Message }) {
  if (m.type === "tool") {
    const p = (m.payload as ToolPayload) || ({} as ToolPayload);
    const r = toolReceipt(p);
    return (
      <div className="text-[13px]">
        <span className="text-ink">{r.icon} {r.label}</span>
        {p.error ? (
          <span className="text-accent"> · {trunc(p.error, 80)}</span>
        ) : r.detail ? (
          <span className="text-muted"> · {r.detail}</span>
        ) : null}
      </div>
    );
  }
  if (m.type === "browser") {
    const p = m.payload as BrowserPayload;
    const kind = p?.type ?? p?.action ?? "event";
    const verb: Record<string, string> = {
      navigate: "打开", click: "点击", type: "输入", scroll: "滚动",
      screenshot: "截图", observe: "观察", done: "完成", action: "操作",
    };
    return (
      <div className="flex items-start gap-2 text-[13px] text-muted">
        {p?.data ? (
          <img
            src={"data:image/png;base64," + p.data}
            alt="frame"
            className="mt-0.5 h-10 w-16 shrink-0 rounded border border-border object-cover"
          />
        ) : (
          <span>🌐</span>
        )}
        <span className="min-w-0">
          <span className="text-ink">{verb[kind] || kind}</span>
          {p?.mode && <span className="text-faint"> ({p.mode})</span>}
          {p?.target && <span className="text-faint"> {trunc(p.target, 56)}</span>}
          {p?.result && <span className="text-faint"> · {trunc(p.result, 56)}</span>}
        </span>
      </div>
    );
  }
  if (m.type === "skill") {
    return <div className="text-[13px] text-muted">✨ 采纳技能</div>;
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
        O
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

const EXAMPLES = [
  { icon: "🌤️", title: "天气卡片", prompt: "西安今天天气怎么样" },
  { icon: "🔎", title: "联网调研", prompt: "使用 researcher 技能,调研 Playwright 和 Puppeteer 的区别,给出带引用的结论" },
  { icon: "🧮", title: "计算 / 换算", prompt: "现在几点了?顺便算 (3+4)*2^3,再把 100 公里换成英里" },
  { icon: "🌐", title: "浏览器自动化", prompt: "用浏览器打开 https://duckduckgo.com/html/ 搜索 Playwright,告诉我第一条结果标题" },
  { icon: "📝", title: "写文件", prompt: "把 Orka 的核心能力整理成一个 markdown 文件 capabilities.md 存到我的工作区" },
  { icon: "🔐", title: "编码 / 哈希", prompt: "把 hello world 做 base64,再算它的 sha256" },
];

function Empty({ onPick }: { onPick: (text: string) => void }) {
  return (
    <div className="flex h-full items-center justify-center px-6">
      <div className="w-full max-w-2xl text-center">
        <div className="mx-auto mb-4 grid h-12 w-12 place-items-center rounded-2xl bg-accent text-white font-serif text-[22px]">
          O
        </div>
        <h1 className="font-serif text-[32px] text-ink">今天想做点什么?</h1>
        <p className="mt-2 text-[14px] text-muted">
          Orka 会自动编排搜索 · 网页 · 天气 · 文件 · 浏览器 · 换算 · 编码等工具。试试:
        </p>
        <div className="mt-6 grid grid-cols-1 gap-2.5 sm:grid-cols-2">
          {EXAMPLES.map((e) => (
            <button
              key={e.title}
              onClick={() => onPick(e.prompt)}
              className="group flex items-start gap-3 rounded-2xl border border-border bg-surface px-4 py-3 text-left transition hover:border-accent/40 hover:bg-surface2"
            >
              <span className="mt-0.5 text-[20px]">{e.icon}</span>
              <span className="min-w-0">
                <span className="block text-[13px] font-medium text-ink">{e.title}</span>
                <span className="mt-0.5 block line-clamp-2 text-[12.5px] text-muted">{e.prompt}</span>
              </span>
            </button>
          ))}
        </div>
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
