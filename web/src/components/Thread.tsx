import { createContext, useContext, useEffect, useRef, useState } from "react";
import type { RunStatus } from "../hooks/useChatStream";
import type { BrowserPayload, ClarifyPayload, Message, ToolPayload, WeatherCardData } from "../types";
import { api, chat as chatApi, files as fileApi } from "../api";
import type { ConfirmPayload } from "../types";
import { Markdown } from "./Markdown";
import { FilePreview } from "./FilePreview";
import { WeatherCard, parseWeatherCard } from "./WeatherCard";
import { Icon, type IconName } from "./Icon";

// Opening a workspace file is shared down the step tree (Steps → Step, AgentLane)
// via context so a filename is clickable wherever it appears without prop drilling.
const OpenFileCtx = createContext<(name: string) => void>(() => {});

// File-producing tools and how to find the file they touched: prefer the explicit
// out/path arg, else scrape a filename out of the result text (e.g. "… → a.pdf").
const FILE_TOOLS = new Set([
  "file_write", "file_read", "doc_export", "doc_read", "chart", "qrcode",
  "csv_to_json", "csv_to_xlsx", "xlsx_to_csv", "csv_join", "sql_query", "pdf_extract", "slides",
]);
// Tools that *create* a workspace file (subset of FILE_TOOLS minus read/list).
// Only these — plus filenames the assistant explicitly names — seed the session
// strip, so a `ls`/file_list/file_read dump doesn't pull in the whole workspace.
const WRITE_TOOLS = new Set([
  "file_write", "doc_export", "chart", "qrcode",
  "csv_to_json", "csv_to_xlsx", "xlsx_to_csv", "csv_join", "slides",
]);
const FILE_RE = /[\w./-]+\.(?:png|jpe?g|gif|webp|svg|pdf|csv|tsv|xlsx?|docx?|md|markdown|txt|json|pptx|html?|py)\b/gi;
function outputFile(p: ToolPayload): string | undefined {
  if (!FILE_TOOLS.has(p.tool || "")) return undefined;
  const a = (p.args || {}) as Record<string, unknown>;
  const explicit = (a.out ?? a.path) == null ? "" : String(a.out ?? a.path).trim();
  if (explicit) return explicit.replace(/^\.?\//, "");
  const m = stripCard(p.result || "").match(FILE_RE);
  return m ? m[m.length - 1] : undefined; // the produced file is usually last
}

// sessionFiles collects the workspace files this conversation produced — the
// basis for the "本会话文件" strip. It scans tool results AND assistant text for
// filename tokens, then keeps only those that actually exist in the workspace
// (`exists` set). The intersection is what ties files to the session: it catches
// shell/python-produced files (chart.png, report.pdf) that aren't in any tool's
// args, while dropping filenames merely mentioned in prose but never created.
function sessionFiles(messages: Message[], exists: Set<string>): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  const add = (raw: string) => {
    const name = raw.replace(/^\.?\//, "").split("/").pop() || "";
    if (name && exists.has(name) && !seen.has(name)) {
      seen.add(name);
      out.push(name);
    }
  };
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    let text = "";
    if (m.type === "tool") {
      const p = m.payload as ToolPayload;
      if (!WRITE_TOOLS.has(p.tool || "")) continue; // ignore read/list/shell dumps
      const a = (p.args || {}) as Record<string, unknown>;
      if (a.out != null) add(String(a.out));
      if (a.path != null) add(String(a.path));
      text = stripCard(p.result || "");
    } else if ((m.type === "chat" || m.type === "stream") && m.role !== "user") {
      text = m.content || ""; // the assistant naming its deliverables
    } else continue;
    const hits = text.match(FILE_RE);
    if (hits) hits.forEach(add);
  }
  return out;
}

type Block =
  | { kind: "user"; m: Message }
  | { kind: "assistant"; m: Message }
  | { kind: "reasoning"; m: Message }
  | { kind: "clarify"; m: Message }
  | { kind: "confirm"; m: Message }
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
    if (m.type === "stream" && m.action === "reasoning") {
      // live "thinking" tokens from a reasoning model → collapsible indicator.
      flush();
      blocks.push({ kind: "reasoning", m });
    } else if (m.type === "chat" || m.type === "stream") {
      // "stream" is the live, transient assistant bubble (token deltas).
      flush();
      blocks.push({ kind: m.role === "user" ? "user" : "assistant", m });
    } else if (m.type === "clarify") {
      flush();
      blocks.push({ kind: "clarify", m });
    } else if (m.type === "confirm") {
      flush();
      blocks.push({ kind: "confirm", m });
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
  onRetry,
  onSchedule,
  fileConv,
}: {
  messages: Message[];
  status: RunStatus;
  onResume: (key: string, answer: string) => void;
  onOpenViewport: () => void;
  onPick: (text: string) => void;
  onRetry: () => void;
  onSchedule: (prompt: string) => void;
  fileConv?: string; // when viewing a shared conversation, read files from its owner via this id
}) {
  const endRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const [previewFile, setPreviewFile] = useState<string | null>(null);
  const [wsFiles, setWsFiles] = useState<Set<string>>(new Set());
  // Smart auto-scroll: only follow new content when the user is already near the
  // bottom, so scrolling up to read history isn't yanked back down.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 120;
    if (nearBottom) endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages.length, status]);
  // Refresh the workspace listing on load and whenever a run settles, so files
  // the agent just produced are recognized by the session strip.
  useEffect(() => {
    if (status === "streaming") return;
    fileApi
      .list(".")
      .then((items) => setWsFiles(new Set(items.filter((i) => !i.dir).map((i) => i.name))))
      .catch(() => {});
  }, [status]);

  const files = sessionFiles(messages, wsFiles);
  const blocks = group(messages);
  const thinking =
    status === "streaming" &&
    (blocks.length === 0 || blocks[blocks.length - 1].kind !== "assistant");

  if (messages.length === 0) return <Empty onPick={onPick} />;

  // Each user turn is a navigable anchor for the floating outline (TOC).
  const turns = blocks
    .filter((b): b is Extract<Block, { kind: "user" }> => b.kind === "user")
    .map((b) => ({ id: "turn-" + b.m.id, text: b.m.content || "" }));

  // Index of the last assistant block — only it gets the "regenerate"/"schedule"
  // actions, since they act on the most recent user turn.
  let lastAssistant = -1;
  let lastUser = -1;
  let lastSteps = -1;
  blocks.forEach((b, i) => {
    if (b.kind === "assistant") lastAssistant = i;
    if (b.kind === "user") lastUser = i;
    if (b.kind === "steps") lastSteps = i;
  });
  const lastUserPrompt = [...blocks].reverse().find((b) => b.kind === "user")?.m.content || "";
  const canAct = status !== "streaming";

  return (
    <OpenFileCtx.Provider value={setPreviewFile}>
    <div ref={scrollRef} className="relative flex-1 overflow-y-auto">
      <ThreadOutline turns={turns} />
      <div className="mx-auto max-w-3xl px-5 py-8">
        {blocks.map((b, i) => (
          <div key={i} id={b.kind === "user" ? "turn-" + b.m.id : undefined} className="rise scroll-mt-4">
            {b.kind === "user" && <UserBubble m={b.m} onEdit={canAct ? onPick : undefined} />}
            {b.kind === "assistant" && (
              <Assistant
                m={b.m}
                live={status === "streaming" && i > lastUser}
                onRegenerate={canAct && i === lastAssistant ? onRetry : undefined}
                onSchedule={canAct && i === lastAssistant && lastUserPrompt ? () => onSchedule(lastUserPrompt) : undefined}
              />
            )}
            {b.kind === "reasoning" && <Reasoning m={b.m} />}
            {b.kind === "clarify" && <Clarify m={b.m} onResume={onResume} />}
            {b.kind === "confirm" && <ConfirmCard m={b.m} />}
            {b.kind === "weather" && <WeatherCard data={b.data} />}
            {b.kind === "steps" && <Steps items={b.items} onOpenViewport={onOpenViewport} live={status === "streaming" && i === lastSteps} />}
          </div>
        ))}
        {thinking && <Thinking />}
        {canAct && status !== "error" && lastAssistant >= 0 && lastUserPrompt && blocks[lastAssistant].kind === "assistant" && (
          <FollowUps
            prompt={lastUserPrompt}
            answer={(blocks[lastAssistant] as Extract<Block, { kind: "assistant" }>).m.content || ""}
            onPick={onPick}
          />
        )}
        {status === "error" && (
          <div className="mb-6 ml-[42px] flex items-center gap-3">
            <button
              onClick={onRetry}
              className="flex items-center gap-1.5 rounded-lg border border-accent/40 bg-accentsoft/50 px-3 py-1.5 text-[13px] text-accent hover:bg-accentsoft transition"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none">
                <path d="M21 12a9 9 0 1 1-3-6.7M21 3v5h-5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
              重试
            </button>
            <span className="text-[12px] text-faint">出错了 — 可重试,或检查工具/沙箱服务是否在运行</span>
          </div>
        )}
        {files.length > 0 && <SessionFiles files={files} onOpen={setPreviewFile} />}
        <div ref={endRef} className="h-2" />
      </div>
      {previewFile && <FilePreview name={previewFile} conv={fileConv} onClose={() => setPreviewFile(null)} />}
    </div>
    </OpenFileCtx.Provider>
  );
}

// SessionFiles pins the workspace files this conversation produced, so they're
// tied to the session instead of lost in the flat global file panel. Click a
// chip to preview (image / pdf / md / text), reusing the shared FilePreview.
function SessionFiles({ files, onOpen }: { files: string[]; onOpen: (name: string) => void }) {
  const icon = (n: string) =>
    /\.(png|jpe?g|gif|webp|svg)$/i.test(n) ? "🖼️"
    : /\.pdf$/i.test(n) ? "📕"
    : /\.(xlsx?|csv|tsv)$/i.test(n) ? "📊"
    : /\.(docx?|md|markdown|txt|rtf)$/i.test(n) ? "📄"
    : /\.pptx$/i.test(n) ? "📑"
    : /\.py$/i.test(n) ? "🐍"
    : "📎";
  return (
    <div className="mb-6 ml-[42px] rounded-xl border border-border bg-surface2/40 p-3">
      <div className="mb-2 text-[11px] font-medium uppercase tracking-wide text-faint">📎 本会话文件 · {files.length}</div>
      <div className="flex flex-wrap gap-1.5">
        {files.map((f) => (
          <button
            key={f}
            onClick={() => onOpen(f)}
            className="flex max-w-full items-center gap-1.5 rounded-lg border border-border bg-surface px-2.5 py-1 text-[12.5px] text-ink hover:border-accent/40 hover:text-accent transition"
            title={"预览 " + f}
          >
            <span className="shrink-0">{icon(f)}</span>
            <span className="truncate">{f}</span>
          </button>
        ))}
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

function UserBubble({ m, onEdit }: { m: Message; onEdit?: (text: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(m.content || "");
  if (editing) {
    const submit = () => {
      const t = draft.trim();
      setEditing(false);
      if (t && t !== m.content) onEdit?.(t);
    };
    return (
      <div className="mb-6 flex justify-end">
        <div className="w-[85%] rounded-2xl bg-userbubble p-2">
          <textarea
            autoFocus
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                submit();
              }
              if (e.key === "Escape") setEditing(false);
            }}
            rows={Math.min(8, draft.split("\n").length + 1)}
            className="w-full resize-none rounded-lg bg-surface px-3 py-2 text-[15px] text-ink outline-none focus:border-accent/50"
          />
          <div className="mt-1.5 flex justify-end gap-2 text-[12px]">
            <button onClick={() => setEditing(false)} className="px-2 py-1 text-faint hover:text-ink">取消</button>
            <button onClick={submit} className="rounded-lg bg-accent px-2.5 py-1 text-white">重新发送</button>
          </div>
        </div>
      </div>
    );
  }
  return (
    <div className="group mb-6 flex flex-col items-end gap-1">
      <div className="max-w-[85%] rounded-2xl rounded-br-md bg-userbubble px-4 py-2.5 text-[15px] leading-relaxed text-ink whitespace-pre-wrap">
        {m.content}
      </div>
      {onEdit && (
        <div className="flex gap-1 opacity-0 transition group-hover:opacity-100">
          <CopyButton text={m.content || ""} />
          <ActionButton label="编辑并重发" onClick={() => { setDraft(m.content || ""); setEditing(true); }}>✎</ActionButton>
        </div>
      )}
    </div>
  );
}

// parsePlan detects the agent's opening numbered plan ("**计划：**\n1. …\n2. …")
// so it can be rendered as a live checklist instead of plain markdown. It only
// fires when a plan-marker header (计划/规划/步骤/方案/plan/steps) is followed by
// ≥2 numbered items, so a final answer that merely contains a numbered list is
// left untouched.
function parsePlan(content: string): { lead: string; steps: string[]; tail: string } | null {
  const lines = (content || "").split("\n");
  let hi = -1;
  for (let i = 0; i < lines.length; i++) {
    const bare = lines[i].replace(/[*#>`]/g, "").trim();
    if (/(^|[^a-zA-Z])(计划|规划|步骤|方案|plan|steps)\s*[:：]?\s*$/i.test(bare)) { hi = i; break; }
  }
  if (hi === -1) return null;
  const steps: string[] = [];
  let last = hi;
  for (let j = hi + 1; j < lines.length; j++) {
    const mm = lines[j].match(/^\s*\d+[.、)]\s+(.*\S)\s*$/);
    if (mm) { steps.push(mm[1].trim()); last = j; }
    else if (lines[j].trim() === "") { if (!steps.length) { last = j; continue; } }
    else if (steps.length) break;
  }
  if (steps.length < 2) return null;
  return {
    lead: lines.slice(0, hi).join("\n").trim(),
    steps,
    tail: lines.slice(last + 1).join("\n").trim(),
  };
}

function PlanChecklist({ steps, live }: { steps: string[]; live: boolean }) {
  return (
    <div className="my-2 rounded-xl border border-border bg-surface2/40 p-3">
      <div className="mb-2 flex items-center gap-2 text-[12.5px] font-medium text-ink">
        <span>🗂️ 执行计划</span>
        {live ? (
          <span className="inline-flex items-center gap-1 text-accent">
            <span className="dot h-1.5 w-1.5 rounded-full bg-accent" /> 进行中
          </span>
        ) : (
          <span className="text-ok">已完成</span>
        )}
      </div>
      <ol className="space-y-1.5">
        {steps.map((s, i) => (
          <li key={i} className="flex items-start gap-2 text-[13px]">
            <span
              className={
                "mt-0.5 grid h-4 w-4 shrink-0 place-items-center rounded-full text-[9px] font-medium " +
                (live ? "border border-faint text-faint" : "bg-ok text-white")
              }
            >
              {live ? i + 1 : "✓"}
            </span>
            <span className={live ? "text-muted" : "text-ink"}>{s}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}

function Assistant({ m, live, onRegenerate, onSchedule }: { m: Message; live?: boolean; onRegenerate?: () => void; onSchedule?: () => void }) {
  const plan = parsePlan(m.content ?? "");
  return (
    <div className="group mb-7 flex gap-3.5">
      <div className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-full bg-accent text-white font-serif text-[13px] leading-none">
        O
      </div>
      <div className="min-w-0 flex-1 pt-0.5">
        {plan ? (
          <>
            {plan.lead && <Markdown>{plan.lead}</Markdown>}
            <PlanChecklist steps={plan.steps} live={!!live} />
            {plan.tail && <Markdown>{plan.tail}</Markdown>}
          </>
        ) : (
          <Markdown>{m.content ?? ""}</Markdown>
        )}
        <div className="mt-1 flex gap-1 opacity-0 transition group-hover:opacity-100">
          <CopyButton text={m.content || ""} />
          {onRegenerate && <ActionButton label="重新生成" onClick={onRegenerate}>↻</ActionButton>}
          {onSchedule && <ActionButton label="把这轮设为定时任务" onClick={onSchedule}>⏰</ActionButton>}
        </div>
      </div>
    </div>
  );
}

function ActionButton({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      aria-label={label}
      title={label}
      className="grid h-6 w-6 place-items-center rounded-md text-[13px] text-faint hover:bg-surface2 hover:text-ink"
    >
      {children}
    </button>
  );
}

function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <ActionButton
      label={done ? "已复制" : "复制"}
      onClick={() => {
        navigator.clipboard?.writeText(text).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        });
      }}
    >
      {done ? "✓" : "⧉"}
    </ActionButton>
  );
}

const AGENT_ICON: Record<string, IconName> = { researcher: "search", writer: "rename", browser: "globe" };

function Steps({ items, onOpenViewport, live }: { items: Message[]; onOpenViewport: () => void; live?: boolean }) {
  // Default-expanded while the run is live (the execution timeline IS the
  // product's differentiator); the user can still collapse, and it auto-folds
  // once the run settles — unless they pinned it open.
  const [pinned, setPinned] = useState<boolean | null>(null);
  const open = pinned ?? !!live;
  const hasShot = items.some((m) => m.type === "browser" && (m.payload as BrowserPayload)?.data);

  // Partition: the orchestrator's own steps render flat; each sub-agent's steps
  // (tagged with meta.agent_id) collapse into their own labeled lane (a swimlane).
  const own = items.filter((m) => !m.meta?.agent_id);
  const lanes = new Map<string, Message[]>();
  for (const m of items) {
    const a = m.meta?.agent_id;
    if (a) {
      if (!lanes.has(a)) lanes.set(a, []);
      lanes.get(a)!.push(m);
    }
  }

  return (
    <div className="mb-6 ml-[42px]">
      <button
        onClick={() => setPinned(!open)}
        className="flex items-center gap-2 rounded-full border border-border bg-surface px-3 py-1.5 text-[13px] text-muted hover:border-accent/40 transition"
      >
        {live ? <Spinner /> : <span className="text-faint">⚙</span>}
        <span>
          {live ? "执行中" : open ? "收起" : "查看"} · {items.length} 步{lanes.size > 0 && ` · ${lanes.size} 个子 Agent`}
        </span>
        {hasShot && (
          <span onClick={(e) => { e.stopPropagation(); onOpenViewport(); }} className="ml-1 text-accent hover:underline">· 看浏览器</span>
        )}
      </button>
      {open && (
        <div className="mt-2 border-l-2 border-border pl-4">
          {own.map((m, i) => (
            <TimelineRow key={m.id} m={m} prev={own[i - 1]} />
          ))}
          {live && (
            <div className="flex items-center gap-2 py-1 text-[12px] text-muted">
              <span className="-ml-[21px] h-2 w-2 animate-pulse rounded-full bg-ok ring-2 ring-surface" />
              进行中…
            </div>
          )}
          {[...lanes.entries()].map(([agent, msgs]) => (
            <AgentLane key={agent} agent={agent} items={msgs} />
          ))}
        </div>
      )}
    </div>
  );
}

// TimelineRow wraps one Step with a status node on the spine + elapsed time, so
// the steps read as a timeline ("图标 + 动作 + 耗时 + 状态") not a flat list.
function TimelineRow({ m, prev }: { m: Message; prev?: Message }) {
  const err = m.type === "tool" && !!(m.payload as ToolPayload)?.error;
  const dur = prev && m.ts && prev.ts ? m.ts - prev.ts : 0;
  return (
    <div className="relative flex items-start gap-2 py-0.5">
      <span
        className={"-ml-[21px] mt-1.5 h-2 w-2 shrink-0 rounded-full ring-2 ring-surface " + (err ? "bg-accent" : "bg-ok/70")}
        title={err ? "失败" : "完成"}
      />
      <div className="min-w-0 flex-1"><Step m={m} /></div>
      {dur > 400 && <span className="shrink-0 pt-0.5 text-[10px] text-faint">{(dur / 1000).toFixed(1)}s</span>}
    </div>
  );
}

// AgentLane groups a delegated sub-agent's steps under its own collapsible card.
function AgentLane({ agent, items }: { agent: string; items: Message[] }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="rounded-lg border border-border bg-surface2/40">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-[13px]"
      >
        <Icon name={AGENT_ICON[agent] || "users"} size={15} className="text-muted" />
        <span className="font-medium text-ink">{agent}</span>
        <span className="text-faint">· {items.length} 步</span>
        <span className="ml-auto text-faint">{open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <div className="space-y-1 border-t border-border px-2.5 py-1.5 pl-4">
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
function toolReceipt(p: ToolPayload): { icon: IconName; label: string; detail: string; file?: string } {
  const a = (p.args || {}) as Record<string, unknown>;
  const s = (k: string) => (a[k] == null ? "" : String(a[k]));
  const res = stripCard(p.result || "");
  const file = outputFile(p); // produced/touched workspace file, if any
  const base: Record<string, { icon: IconName; label: string; detail: string }> = {
    file_write: { icon: "file", label: "写入", detail: res },
    file_read: { icon: "file", label: "读取", detail: trunc(res, 70) },
    file_list: { icon: "folder", label: `列目录 ${s("path") || "/"}`, detail: trunc(res, 70) },
    web_search: { icon: "search", label: `搜索 “${s("query")}”`, detail: trunc(res, 70) },
    fetch_url: { icon: "link", label: "读取网页", detail: trunc(s("url") || res, 70) },
    weather: { icon: "sun", label: `天气 ${s("location")}`, detail: "" },
    current_time: { icon: "clock", label: "当前时间", detail: trunc(res, 60) },
    calculator: { icon: "calc", label: "计算", detail: trunc(res, 60) },
    unit_convert: { icon: "ruler", label: "单位换算", detail: trunc(res, 60) },
    http_request: { icon: "globe", label: `HTTP ${s("method") || "GET"}`, detail: trunc(s("url"), 60) },
    apply_skill: { icon: "sparkle", label: `采纳技能 ${s("name")}`, detail: "" },
    // Office / data / code tools.
    doc_export: { icon: "file", label: "导出文档", detail: trunc(res, 60) },
    doc_read: { icon: "book", label: "读取文档", detail: trunc(res, 60) },
    chart: { icon: "chart", label: "生成图表", detail: trunc(res, 60) },
    qrcode: { icon: "qr", label: "二维码", detail: trunc(res, 60) },
    csv_query: { icon: "table", label: "查询表格", detail: trunc(res, 60) },
    csv_stats: { icon: "table", label: "统计表格", detail: trunc(res, 60) },
    csv_to_json: { icon: "table", label: "CSV→JSON", detail: trunc(res, 60) },
    csv_to_xlsx: { icon: "table", label: "CSV→Excel", detail: trunc(res, 60) },
    xlsx_to_csv: { icon: "table", label: "Excel→CSV", detail: trunc(res, 60) },
    csv_join: { icon: "link", label: "连接表格", detail: trunc(res, 60) },
    sql_query: { icon: "table", label: "SQL 查询", detail: trunc(res, 60) },
    pdf_extract: { icon: "book", label: "提取 PDF", detail: trunc(res, 60) },
    slides: { icon: "deck", label: "生成 PPT", detail: trunc(res, 60) },
    python: { icon: "code", label: "运行 Python", detail: trunc(res, 70) },
    currency: { icon: "coin", label: "汇率换算", detail: trunc(res, 60) },
    timezone: { icon: "clock", label: "时区换算", detail: trunc(res, 60) },
  };
  if (p.tool === "researcher" || p.tool === "writer" || p.tool === "browser") {
    return { icon: "users", label: `委派 ${p.tool}`, detail: trunc(s("task") || res, 64), file };
  }
  const r = base[p.tool || ""] || { icon: "wrench" as IconName, label: p.tool || "tool", detail: trunc(res, 70) };
  return { ...r, file };
}

function Step({ m }: { m: Message }) {
  const openFile = useContext(OpenFileCtx);
  if (m.type === "tool") {
    const p = (m.payload as ToolPayload) || ({} as ToolPayload);
    const r = toolReceipt(p);
    return (
      <div className="text-[13px]">
        <span className="inline-flex items-center gap-1.5 align-middle text-ink"><Icon name={r.icon} size={14} className="shrink-0 text-muted" /> {r.label} </span>
        {r.file ? (
          <button
            onClick={(e) => {
              e.stopPropagation();
              openFile(r.file!);
            }}
            className="text-accent hover:underline"
            title="点击预览文件"
          >
            {r.file} ↗
          </button>
        ) : null}
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
          <Icon name="globe" size={14} className="mt-0.5 text-muted" />
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
    return <div className="flex items-center gap-1.5 text-[13px] text-muted"><Icon name="sparkle" size={14} /> 采纳技能</div>;
  }
  return (
    <div className="text-[13px] text-muted">
      <span className="font-mono">{m.type}</span> {m.action} {trunc(m.content, 80)}
    </div>
  );
}

const TOOL_LABEL: Record<string, string> = { shell: "终端命令", run_agent: "浏览器操作", http_request: "网络请求", python: "运行代码" };

// ConfirmCard is the human-in-the-loop gate: a side-effecting tool call is
// paused until the user approves or rejects it.
function ConfirmCard({ m }: { m: Message }) {
  const p = m.payload as ConfirmPayload;
  const [done, setDone] = useState<"" | "once" | "always" | "reject">("");
  const decide = async (approve: boolean, always: boolean) => {
    if (done) return;
    setDone(approve ? (always ? "always" : "once") : "reject");
    try {
      await chatApi.confirm(p.id, approve, always);
    } catch {
      setDone(""); // let them retry on failure
    }
  };
  const doneLabel =
    done === "always" ? "✅ 本会话始终允许 · 继续执行" : done === "once" ? "✅ 已允许一次 · 继续执行" : "🚫 已拒绝,跳过该操作";
  return (
    <div className="mb-6 ml-[42px] rounded-xl border border-accent/40 bg-accentsoft/40 p-3.5">
      <div className="mb-1 flex items-center gap-1.5 text-[13px] font-medium text-ink">
        <Icon name="shield" size={14} className="text-accent" /> 需要你确认 · {TOOL_LABEL[p.tool] || p.tool}
      </div>
      <div className="mb-2.5 break-words rounded-lg bg-surface/70 px-2.5 py-1.5 font-mono text-[12px] text-muted">{p.summary}</div>
      {done ? (
        <div className="text-[12.5px] text-faint">{doneLabel}</div>
      ) : (
        <div className="flex flex-wrap gap-2">
          <button onClick={() => decide(true, false)} className="rounded-lg bg-accent px-3 py-1.5 text-[12.5px] text-white hover:opacity-90">允许一次</button>
          <button onClick={() => decide(true, true)} className="rounded-lg border border-accent/40 bg-surface px-3 py-1.5 text-[12.5px] text-ink hover:bg-accentsoft/60" title={`本会话内不再询问“${TOOL_LABEL[p.tool] || p.tool}”`}>本会话始终允许</button>
          <button onClick={() => decide(false, false)} className="rounded-lg border border-border bg-surface px-3 py-1.5 text-[12.5px] text-muted hover:border-accent/40">拒绝</button>
        </div>
      )}
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

// Reasoning renders a reasoning model's live "thinking" tokens as a compact,
// collapsible indicator — so a long reasoning call shows visible progress.
function Reasoning({ m }: { m: Message }) {
  const [open, setOpen] = useState(true);
  const endRef = useRef<HTMLDivElement>(null);
  const text = m.content || "";
  useEffect(() => {
    if (open) endRef.current?.scrollIntoView({ block: "nearest" });
  }, [text, open]);
  return (
    <div className="mb-4 ml-[42px]">
      <button onClick={() => setOpen((o) => !o)} className="flex items-center gap-1.5 text-[12px] text-faint hover:text-muted">
        <span className="dot h-1.5 w-1.5 rounded-full bg-accent" />
        <span>💭 思考中{open ? "" : "…"}</span>
        <span className="text-[10px]">{open ? "▾" : "▸"}</span>
      </button>
      {open && text && (
        <div className="mt-1.5 max-h-32 overflow-y-auto whitespace-pre-wrap rounded-lg border border-border bg-surface2/40 px-2.5 py-2 text-[11.5px] leading-relaxed text-faint">
          {text}
          <div ref={endRef} />
        </div>
      )}
    </div>
  );
}

// FollowUps shows 2–3 suggested next questions under the latest answer (Perplexity
// style); clicking one sends it. Fetched lazily from the mini model once the turn
// settles, keyed by the answer so it refreshes per turn.
function FollowUps({ prompt, answer, onPick }: { prompt: string; answer: string; onPick: (t: string) => void }) {
  const [items, setItems] = useState<string[]>([]);
  useEffect(() => {
    let cancelled = false;
    setItems([]);
    if (!answer.trim()) return;
    api.followups(prompt, answer).then((r) => { if (!cancelled) setItems(r.suggestions || []); }).catch(() => {});
    return () => { cancelled = true; };
  }, [prompt, answer]);
  if (items.length === 0) return null;
  return (
    <div className="rise mb-6 ml-[42px]">
      <div className="mb-1.5 text-[11px] text-faint">追问</div>
      <div className="flex flex-col items-start gap-1.5">
        {items.map((q, i) => (
          <button
            key={i}
            onClick={() => onPick(q)}
            className="group max-w-full rounded-xl border border-border bg-surface px-3 py-1.5 text-left text-[13px] text-muted transition hover:border-accent/40 hover:bg-surface2 hover:text-ink"
          >
            <span className="mr-1 text-accent opacity-60 group-hover:opacity-100">+</span>
            {q}
          </button>
        ))}
      </div>
    </div>
  );
}

const EXAMPLES = [
  {
    icon: "🔭",
    cat: "深度调研",
    title: "调研并产出对比报告",
    desc: "多源交叉验证,写一份带引用的对比报告并存档",
    steps: ["联网搜索", "读取网页", "写文件"],
    prompt: "用 researcher 技能调研 2025 年主流 AI Agent 框架(LangGraph、Eino、AutoGen)的设计差异,交叉验证至少两个来源,写一份带引用的对比报告并存为 agent-frameworks.md",
    tint: ["#b48ee6", "#8b5cf6"],
  },
  {
    icon: "🔗",
    cat: "自动化管线",
    title: "多步管线一条龙",
    desc: "多城市抓取 → 单位换算 → 整理成表格存档",
    steps: ["天气 ×3", "单位换算", "写文件"],
    prompt: "查北京、上海、西安今天的天气,把温度换算成华氏度,整理成一张 Markdown 表格存到工作区 weather.md",
    tint: ["#3f9d5a", "#7bc88f"],
  },
  {
    icon: "🕸️",
    cat: "浏览器",
    title: "抓取实时网页并归纳",
    desc: "打开真实站点,抓取榜单并按主题总结要点",
    steps: ["启动浏览器", "抓取内容", "归纳总结"],
    prompt: "用浏览器打开 https://news.ycombinator.com 抓取首页前 10 条标题,挑出与 AI 相关的,逐条总结要点",
    tint: ["#7db4f0", "#3f7fd8"],
  },
  {
    icon: "🔮",
    cat: "创意分析",
    title: "玄学 × 大数据预测",
    desc: "传统五行生肖结合真实数据的趣味推演报告",
    steps: ["联网检索", "数据分析", "生成报告"],
    prompt: "结合中国传统算命理论(五行、生肖)和近几届世界杯的真实数据,分析本届夺冠热门球队,给出一份有理有据又有趣的预测报告",
    tint: ["#e8943f", "#d4674a"],
    playful: true,
  },
];

function Empty({ onPick }: { onPick: (text: string) => void }) {
  const [showMore, setShowMore] = useState(false);
  // Lead with work-oriented scenarios; keep the playful one behind 探索更多.
  const shown = EXAMPLES.filter((e) => showMore || !(e as { playful?: boolean }).playful);
  const hiddenCount = EXAMPLES.length - EXAMPLES.filter((e) => !(e as { playful?: boolean }).playful).length;
  return (
    <div className="relative flex h-full items-center justify-center overflow-hidden px-6 py-10">
      {/* animated aurora backdrop */}
      <div aria-hidden className="pointer-events-none absolute inset-0 overflow-hidden">
        <div className="aurora absolute left-[12%] top-[8%] h-72 w-72 rounded-full bg-accent/25 blur-[90px]" />
        <div className="aurora-2 absolute right-[10%] top-[22%] h-80 w-80 rounded-full bg-[#e8943f]/20 blur-[100px]" />
        <div className="aurora-3 absolute bottom-[6%] left-[34%] h-72 w-72 rounded-full bg-[#8b5cf6]/12 blur-[100px]" />
      </div>

      <div className="relative w-full max-w-2xl text-center">
        <div className="rise mx-auto mb-4 inline-flex items-center gap-1.5 rounded-full border border-border bg-surface/70 px-3 py-1 text-[11px] font-medium text-muted backdrop-blur">
          <span className="text-accent">⚡</span> AI 自动化执行平台
        </div>
        <div className="halo rise mx-auto mb-5 grid h-16 w-16 place-items-center rounded-[22px] bg-gradient-to-br from-[#e07a52] to-[#c45c3e] font-serif text-[28px] text-white ring-1 ring-black/5">
          O
        </div>
        <h1 className="grad-text rise font-serif text-[38px] font-medium leading-tight">交给 Orka 去执行</h1>
        <p className="rise mx-auto mt-3 max-w-lg text-[14px] leading-relaxed text-muted">
          描述一个目标,它会<span className="text-ink">自己拆解步骤、联网调研、调用工具</span>,
          跑完整条链路再把结果交给你。挑一个复杂任务试试:
        </p>

        <div className="mt-8 grid grid-cols-1 gap-3.5 sm:grid-cols-2">
          {shown.map((e, i) => (
            <button
              key={e.title}
              onClick={() => onPick(e.prompt)}
              style={{ animationDelay: `${100 + i * 60}ms` }}
              className="rise group relative flex items-start gap-3.5 overflow-hidden rounded-2xl border border-border bg-surface/70 px-4 py-4 text-left shadow-sm backdrop-blur transition-all duration-300 hover:-translate-y-1 hover:border-accent/30 hover:shadow-[0_14px_36px_rgba(40,38,32,0.12)]"
            >
              <span className="sheen" />
              {/* category accent bar that grows on hover */}
              <span
                aria-hidden
                className="absolute left-0 top-1/2 h-8 w-[3px] -translate-y-1/2 rounded-r-full opacity-60 transition-all duration-300 group-hover:h-16 group-hover:opacity-100"
                style={{ background: `linear-gradient(${e.tint[0]}, ${e.tint[1]})` }}
              />
              <span
                className="relative grid h-11 w-11 shrink-0 place-items-center rounded-xl text-[20px] text-white shadow-sm transition-transform duration-300 group-hover:scale-110 group-hover:-rotate-6"
                style={{ background: `linear-gradient(135deg, ${e.tint[0]}, ${e.tint[1]})`, boxShadow: `0 6px 16px ${e.tint[1]}44` }}
              >
                <span className="drop-shadow-sm">{e.icon}</span>
              </span>
              <span className="relative min-w-0 flex-1">
                <span className="flex items-center gap-1.5">
                  <span className="block text-[14px] font-semibold text-ink">{e.title}</span>
                  <span
                    className="rounded-full px-1.5 py-px text-[10px] font-medium"
                    style={{ color: e.tint[1], background: `${e.tint[1]}1a` }}
                  >
                    {e.cat}
                  </span>
                </span>
                <span className="mt-1 block text-[12.5px] leading-snug text-muted">{e.desc}</span>
                <span className="mt-2 flex flex-wrap items-center gap-1">
                  {e.steps.map((s, j) => (
                    <span key={s} className="flex items-center gap-1">
                      {j > 0 && <span className="text-[9px] text-faint">→</span>}
                      <span className="rounded-md bg-surface2/80 px-1.5 py-0.5 text-[10px] text-faint transition-colors group-hover:text-muted">
                        {s}
                      </span>
                    </span>
                  ))}
                </span>
              </span>
              <span className="relative mt-0.5 shrink-0 translate-x-1 text-[14px] text-faint opacity-0 transition-all duration-300 group-hover:translate-x-0 group-hover:text-accent group-hover:opacity-100">
                →
              </span>
            </button>
          ))}
        </div>

        {hiddenCount > 0 && !showMore && (
          <button onClick={() => setShowMore(true)} className="rise mt-4 text-[12px] text-faint hover:text-accent" style={{ animationDelay: "440ms" }}>
            探索更多示例 →
          </button>
        )}

        <p className="rise mt-6 text-[12px] text-faint" style={{ animationDelay: "480ms" }}>
          点卡片直接跑,或在下方描述你自己的任务
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
