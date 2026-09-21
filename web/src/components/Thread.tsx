import { executionError, normalizeToolReceipt, totalToolElapsedMs } from '../lib/executionResult';
import { steeringReceipt } from '../lib/steeringReceipt';
import { HomeWelcome } from "./HomeWelcome";
import { ActionChip } from './ActionChip';
import { useDeliveryManifest } from '../hooks/useDeliveryManifest';
import type { DeliverySnapshot } from '../lib/runEvidence';
import { useCallback, createContext, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { RunStatus } from "../hooks/useChatStream";
import type { BrowserPayload, ClarifyPayload, Message, ToolPayload, WeatherCardData } from "../types";
import { api, chat as chatApi, files as fileApi } from "../api";
import type { ConfirmPayload, PlanPayload } from "../types";
import { normalizeWorkspacePath, workspaceLinkPath } from "../lib/sessionFiles";
import { useSessionFiles } from "../hooks/useSessionFiles";
import { executionIdentity, isIncompleteRun, pendingConfirmationID, type RecoverySnapshot } from "../lib/runRecovery";
import { Markdown } from "./Markdown";
import { FilePreview } from "./FilePreview";
import { FollowUps } from "./FollowUps";
import { WeatherCard, parseWeatherCard } from "./WeatherCard";
import { Icon, type IconName } from "./Icon";
import { confirmDialog } from "../lib/confirm";
import { toast, toastError } from "../lib/toast";

// Opening a workspace file is shared down the step tree (Steps → Step, AgentLane)
// via context so a filename is clickable wherever it appears without prop drilling.
const FileScopeCtx = createContext({ conversationID: "", ownerEmail: "", readOnly: true });
const DeliveryManifestCtx = createContext<DeliverySnapshot[]>([]);
const OpenFileCtx = createContext<(name: string, opts?: { history?: boolean }) => void>(() => {});

// File-producing tools and how to find the file they touched: prefer the explicit
// out/path arg, else scrape a filename out of the result text (e.g. "… → a.pdf").
const FILE_TOOLS = new Set([
  "file_write", "file_read", "doc_export", "doc_read", "chart", "qrcode",
  "csv_to_json", "csv_to_xlsx", "xlsx_to_csv", "csv_join", "sql_query", "pdf_extract", "slides",
]);
const FILE_RE = /[\w./-]+\.(?:png|jpe?g|gif|webp|svg|pdf|csv|tsv|xlsx?|docx?|md|markdown|txt|json|pptx|html?|py)\b/gi;
function outputFile(p: ToolPayload): string | undefined {
  if (!FILE_TOOLS.has(p.tool || "")) return undefined;
  const a = (p.args || {}) as Record<string, unknown>;
  const explicit = (a.out ?? a.path) == null ? "" : String(a.out ?? a.path).trim();
  if (explicit) return explicit;
  const m = stripCard(p.result || "").match(FILE_RE);
  return m ? m[m.length - 1] : undefined; // the produced file is usually last
}

type Block =
  | { kind: "user"; m: Message }
  | { kind: "assistant"; m: Message }
  | { kind: "reasoning"; m: Message }
  | { kind: "clarify"; m: Message }
  | { kind: "confirm"; m: Message }
  | { kind: "plan"; m: Message }
  | { kind: "weather"; data: WeatherCardData }
  | { kind: "steps"; items: Message[] };

function group(messages: Message[]): Block[] {
  const blocks: Block[] = [];
  let buf: Message[] = [];
  let planBlock: Extract<Block, { kind: "plan" }> | null = null;
  const flush = () => {
    if (buf.length) {
      blocks.push({ kind: "steps", items: buf });
      buf = [];
    }
  };
  for (const m of messages) {
    if (buf.length && buf[buf.length - 1].meta?.run_id !== m.meta?.run_id) flush();
    if (m.type === "task" || m.type === "heartbeat") continue;
    if (m.type === "stream" && m.action === "reasoning") {
      // live "thinking" tokens from a reasoning model → collapsible indicator.
      flush();
      blocks.push({ kind: "reasoning", m });
    } else if (m.type === "chat" || m.type === "stream") {
      // "stream" is the live, transient assistant bubble (token deltas).
      flush();
      if (m.role === "user") planBlock = null;
      blocks.push({ kind: m.role === "user" ? "user" : "assistant", m });
    } else if (m.type === "clarify") {
      flush();
      blocks.push({ kind: "clarify", m });
    } else if (m.type === "confirm") {
      flush();
      blocks.push({ kind: "confirm", m });
    } else if (m.type === "plan") {
      // The agent re-emits the WHOLE plan on every update (idempotent snapshot).
      // Render a single live checklist that updates in place: keep the block at
      // the first plan's position and point it at the latest snapshot.
      if (planBlock && (!m.meta?.run_id || planBlock.m.meta?.run_id === m.meta.run_id)) {
        planBlock.m = m;
      } else {
        flush();
        planBlock = { kind: "plan", m };
        blocks.push(planBlock);
      }
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
  onResumed,
  bottomInset = 0,
  onPick,
  onExample,
  onRetry,
  onSchedule,
  onFork,
  fileConv: sharedFileConv,
  conversationID,
  ownerEmail,
  recovery,
  onContinue,
  canRetry,
}: {
  conversationID: string;
  ownerEmail: string;
  recovery: RecoverySnapshot;
  onContinue: () => void;
  canRetry: boolean;
  messages: Message[];
  status: RunStatus;
  onResume: (key: string, answer: string) => void;
  // Re-attach to a conversation whose paused run the server just resumed.
  onResumed?: (cid: string) => void;
  // Height of the floating composer, reserved as bottom padding so the last
  // message can always be scrolled clear of it.
  bottomInset?: number;
  onPick: (text: string) => void;
  onExample?: (text: string) => void;
  onRetry: () => void;
  onSchedule: (prompt: string) => void;
  onFork?: (messageID: string) => void;
  fileConv?: string; // when viewing a shared conversation, read files from its owner via this id
}) {
  const endRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const [previewFile, setPreviewFile] = useState<{ name: string; conversationID: string; history?: boolean } | null>(null);
  const fileConv = conversationID;
  const readOnlyFiles = !!sharedFileConv;
  const openFile = (raw: string, opts?: { history?: boolean }) => {
    const name = normalizeWorkspacePath(raw, ownerEmail, fileConv);
    if (name && fileConv) setPreviewFile({ name, conversationID: fileConv, history: opts?.history });
  };
  useEffect(() => { setPreviewFile(null); }, [conversationID]);
  const deliveryRunKey = useMemo(() => [...new Set(messages.filter(m =>
    m.role === "assistant" && m.meta?.run_id && /\[[^\]]*\]\([^)]+\)/.test(m.content || "")
  ).map(m => m.meta.run_id))].sort().join(","), [messages]);
  const deliveryManifest = useDeliveryManifest(fileConv, deliveryRunKey, status);
  const files = useSessionFiles(messages, { conversationID: fileConv, ownerEmail }, status);
  // Smart auto-scroll: only follow new content when the user is already near the
  // bottom, so scrolling up to read history isn't yanked back down.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 120;
    // Jump instantly rather than animating: a smooth scrollIntoView keeps
    // re-targeting while tokens stream in, which fights a user dragging the
    // scrollbar and makes the thread feel like it won't settle at the bottom.
    if (nearBottom) el.scrollTop = el.scrollHeight;
  }, [messages.length, status]);
  const blocks = useMemo(() => group(messages), [messages]);
  const pendingConfirm = useMemo(() => pendingConfirmationID(messages, conversationID), [messages, conversationID]);
  const thinking =
    status === "streaming" &&
    (blocks.length === 0 || blocks[blocks.length - 1].kind !== "assistant");

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
  const activeExecution = executionIdentity({ conversationID, messages, status });
  const canAct = status !== "streaming" && !recovery.busy;
  const failed = isIncompleteRun({ conversationID, messages, status }, recovery.run);

  // In-thread find: scan the loaded conversation for a query and jump between
  // hits. Frontend-only — searches the user/assistant/reasoning text already in
  // memory (cross-conversation search would need a backend index).
  const [findOpen, setFindOpen] = useState(false);
  const [findQuery, setFindQuery] = useState("");
  const [findIdx, setFindIdx] = useState(0);
  const matches = (() => {
    const q = findQuery.trim().toLowerCase();
    if (!q) return [] as number[];
    const out: number[] = [];
    blocks.forEach((b, i) => {
      const t = "m" in b ? b.m.content || "" : "";
      if (t.toLowerCase().includes(q)) out.push(i);
    });
    return out;
  })();
  const curMatch = matches.length ? matches[Math.min(findIdx, matches.length - 1)] : -1;
  useEffect(() => {
    if (curMatch >= 0) document.getElementById("block-" + curMatch)?.scrollIntoView({ behavior: "smooth", block: "center" });
  }, [curMatch]);
  // ⌘/Ctrl+F opens the in-thread find (overrides the browser find — this is a
  // full workbench, so an app-level find over the conversation is more useful).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "f") {
        e.preventDefault();
        setFindOpen(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // Render cap: a very long conversation only mounts its most recent slice;
  // older turns fold behind a button. Full virtualization would fight the find
  // bar / outline anchors, so this keeps every rendered node real while bounding
  // DOM size. Finding forces the full list so any match can be scrolled to.
  const RENDER_CAP = 40;
  const [showAll, setShowAll] = useState(false);
  const finding = findQuery.trim().length > 0;
  const startIdx = blocks.length > RENDER_CAP && !showAll && !finding ? blocks.length - RENDER_CAP : 0;

  // Early return AFTER every hook above — otherwise the empty state and a loaded
  // conversation would run a different number of hooks (Rules of Hooks).
  if (messages.length === 0) return <HomeWelcome onPick={onExample || onPick} bottomInset={bottomInset} />;

  return (
    <FileScopeCtx.Provider value={{ conversationID: fileConv, ownerEmail, readOnly: readOnlyFiles }}>
    <DeliveryManifestCtx.Provider value={deliveryManifest}>
    <OpenFileCtx.Provider value={openFile}>
    <div ref={scrollRef} className="relative min-h-0 flex-1 overflow-y-auto">
      <ThreadOutline turns={turns} />
      <ThreadFind
        open={findOpen}
        query={findQuery}
        count={matches.length}
        idx={matches.length ? Math.min(findIdx, matches.length - 1) : 0}
        onQuery={(q) => { setFindQuery(q); setFindIdx(0); }}
        onStep={(d) => setFindIdx((n) => { const len = matches.length; if (!len) return 0; return (n + d + len) % len; })}
        onOpen={() => setFindOpen(true)}
        onClose={() => { setFindOpen(false); setFindQuery(""); }}
      />
      <div className="mx-auto max-w-3xl px-5 pt-8" style={{ paddingBottom: Math.max(bottomInset + 24, 96) }}>
        {startIdx > 0 && (
          <div className="mb-6 text-center">
            <button
              onClick={() => setShowAll(true)}
              className="rounded-full border border-border bg-surface px-3 py-1.5 text-[12.5px] text-muted transition hover:border-accent/40 hover:text-accent"
            >
              显示更早的 {startIdx} 条
            </button>
          </div>
        )}
        {blocks.slice(startIdx).map((b, k) => {
          const i = startIdx + k;
          return (
          <div key={i} id={"block-" + i} className={"rise scroll-mt-4 " + (i === curMatch ? "rounded-2xl ring-2 ring-accent/60 ring-offset-4 ring-offset-bg" : "")}>
            {b.kind === "user" && <span id={"turn-" + b.m.id} className="block h-0 scroll-mt-4" aria-hidden />}
            {b.kind === "user" && <UserBubble m={b.m} onEdit={canAct ? onPick : undefined} onFork={canAct && onFork ? () => onFork(b.m.id) : undefined} />}
            {b.kind === "assistant" && (
              <Assistant
                m={b.m}
                live={status === "streaming" && i > lastUser}
                onRegenerate={canAct && canRetry && i === lastAssistant ? onRetry : undefined}
                onSchedule={canAct && i === lastAssistant && lastUserPrompt ? () => onSchedule(lastUserPrompt) : undefined}
              />
            )}
            {b.kind === "reasoning" && <Reasoning m={b.m} />}
            {b.kind === "clarify" && <Clarify m={b.m} onResume={onResume} />}
            {b.kind === "confirm" && <ConfirmCard m={b.m} active={b.m.id === pendingConfirm} onResumed={onResumed} />}
            {b.kind === "plan" && <StructuredPlan plan={(b.m.payload as PlanPayload) ?? { steps: [] }} live={status === "streaming" && i >= lastUser} />}
            {b.kind === "weather" && <WeatherCard data={b.data} />}
            {b.kind === "steps" && <Steps items={b.items} live={status === "streaming" && i === lastSteps && (activeExecution?.run ? b.items.some(m => m.meta?.run_id === activeExecution.run) : i > lastUser)} />}
          </div>
          );
        })}
        {thinking && <Thinking />}
        {!readOnlyFiles && canAct && !failed && lastAssistant > lastUser && lastUserPrompt && blocks[lastAssistant].kind === "assistant" && (
          <FollowUps ownerEmail={ownerEmail}
            conversationID={conversationID}
            runID={(blocks[lastAssistant] as Extract<Block, { kind: "assistant" }>).m.meta.run_id || ""}
            prompt={lastUserPrompt}
            answer={(blocks[lastAssistant] as Extract<Block, { kind: "assistant" }>).m.content || ""}
            selectedVersion={(blocks[lastAssistant] as Extract<Block, { kind: "assistant" }>).m.meta.model_version || "auto"}
            modelProfile={(blocks[lastAssistant] as Extract<Block, { kind: "assistant" }>).m.meta.model_profile || ""}
            onPick={onPick}
          />
        )}
        {failed && (
          <div className="mb-6 ml-[42px] space-y-2" role="status">
            <div className="flex flex-wrap items-center gap-3">
              {(recovery.recoverable || recovery.busy) && (
                <ActionChip onClick={onContinue} disabled={recovery.busy} icon="refresh">
                  {recovery.busy ? "正在继续任务…" : "继续未完成任务"}
                </ActionChip>
              )}
            </div>
            <p className="text-[12px] text-faint">
              {recovery.error || (recovery.busy ? "正在连接续跑，请稍候。" : recovery.recoverable ? "继续任务会保留已完成的进度。" : recovery.checking ? "正在检查是否可以继续任务…" : "本次执行未完成。")}
            </p>
          </div>
        )}
        {files.length > 0 && <SessionFiles files={files} onOpen={(n) => openFile(n)} />}
        <div ref={endRef} className="h-2" />
      </div>
      {previewFile && previewFile.conversationID === fileConv && <FilePreview key={fileConv + ":" + previewFile.name} name={previewFile.name} conv={fileConv} readOnly={readOnlyFiles} initialHistory={previewFile.history} onClose={() => setPreviewFile(null)} />}
    </div>
    </OpenFileCtx.Provider>
    </DeliveryManifestCtx.Provider>
    </FileScopeCtx.Provider>
  );
}

// SessionFiles pins the workspace files this conversation produced, so they're
// tied to the session instead of lost in the flat global file panel. Click a
// chip to preview (image / pdf / md / text), reusing the shared FilePreview.
function SessionFiles({ files, onOpen }: { files: string[]; onOpen: (name: string) => void }) {
  const scope = useContext(FileScopeCtx);
  const [expanded, setExpanded] = useState(false);
  useEffect(() => setExpanded(false), [scope.conversationID]);
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
      <div className="flex max-h-64 flex-wrap gap-1.5 overflow-y-auto">
        {(expanded ? files : files.slice(0, 8)).map((f) => (
          <div key={f} className="flex max-w-full items-center rounded-full border border-border bg-surface">
          <button
            onClick={() => onOpen(f)}
            className="flex min-w-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-[12.5px] text-ink hover:border-accent/40 hover:text-accent transition"
            title={"预览 " + f}
          >
            <span className="shrink-0">{icon(f)}</span>
            <span className="truncate">{f}</span>
          </button>
          <a className="shrink-0 p-2 text-muted hover:text-accent" href={fileApi.downloadURL(f, scope.conversationID)} aria-label={"下载 " + f}><Icon name="download" size={13}/></a>
          </div>
        ))}
      </div>
      {files.length > 8 && <ActionChip className="mt-2" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>
        {expanded ? '收起文件' : `查看全部 ${files.length} 个文件`}
      </ActionChip>}
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

// ThreadFind is the in-conversation find bar (⌘F): type a query to jump between
// matching turns with prev/next, like a browser find scoped to this thread.
function ThreadFind({
  open, query, count, idx, onQuery, onStep, onOpen, onClose,
}: {
  open: boolean;
  query: string;
  count: number;
  idx: number;
  onQuery: (q: string) => void;
  onStep: (d: number) => void;
  onOpen: () => void;
  onClose: () => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => { if (open) inputRef.current?.focus(); }, [open]);
  if (!open) {
    return (
      <div className="sticky top-0 z-10 float-left ml-2 mt-2">
        <button onClick={onOpen} title="在对话中查找 (⌘F)" aria-label="在对话中查找" className="grid h-8 w-8 place-items-center rounded-lg border border-border bg-surface text-muted transition hover:bg-surface2">
          <Icon name="search" size={16} />
        </button>
      </div>
    );
  }
  return (
    <div className="sticky top-0 z-20 float-left ml-2 mt-2">
      <div className="flex items-center gap-1 rounded-lg border border-border bg-surface px-1.5 py-1 shadow-lg">
        <Icon name="search" size={14} className="text-faint" />
        <input
          ref={inputRef}
          value={query}
          onChange={(e) => onQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") { e.preventDefault(); onStep(e.shiftKey ? -1 : 1); }
            if (e.key === "Escape") { e.preventDefault(); onClose(); }
          }}
          placeholder="在对话中查找…"
          className="w-40 bg-transparent text-[13px] outline-none placeholder:text-faint"
        />
        <span className="min-w-[36px] text-center text-[11px] text-faint">{query ? (count ? `${idx + 1}/${count}` : "0") : ""}</span>
        <button onClick={() => onStep(-1)} disabled={!count} aria-label="上一个匹配" className="grid h-6 w-6 place-items-center rounded text-faint hover:bg-surface2 disabled:opacity-30"><Icon name="chevron" size={13} className="rotate-180" /></button>
        <button onClick={() => onStep(1)} disabled={!count} aria-label="下一个匹配" className="grid h-6 w-6 place-items-center rounded text-faint hover:bg-surface2 disabled:opacity-30"><Icon name="chevron" size={13} /></button>
        <button onClick={onClose} aria-label="关闭查找" className="grid h-6 w-6 place-items-center rounded text-faint hover:bg-surface2"><Icon name="close" size={13} /></button>
      </div>
    </div>
  );
}

function UserBubble({ m, onEdit, onFork }: { m: Message; onEdit?: (text: string) => void; onFork?: () => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(m.content || "");
  const receipt = steeringReceipt(m);
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
      {receipt && <span className="text-[11px] text-faint">{receipt}</span>}
      {(onEdit || onFork) && (
        <div className="flex gap-1 opacity-0 transition group-hover:opacity-100">
          <CopyButton text={m.content || ""} />
          {onEdit && <ActionButton label="编辑并重发" onClick={() => { setDraft(m.content || ""); setEditing(true); }}><Icon name="rename" size={13} /></ActionButton>}
          {onFork && <ActionButton label="从这里分支(复制到此为止的对话,另开一条探索)" onClick={onFork}><Icon name="share" size={13} className="rotate-90" /></ActionButton>}
        </div>
      )}
    </div>
  );
}

// StructuredPlan renders a first-class plan event (the agent's `update_plan`
// tool calls) as a live checklist with real per-step status — pending / active
// (currently working) / done / blocked — instead of inferring progress from prose.
function StructuredPlan({ plan, live }: { plan: PlanPayload; live: boolean }) {
  const steps = plan.steps || [];
  if (steps.length === 0) return null;
  const done = steps.filter((s) => s.status === "done").length;
  const blocked = steps.filter((s) => s.status === "blocked").length;
  const allDone = done === steps.length;
  return (
    <div className="my-2 ml-[42px] rounded-xl border border-border bg-surface2/40 p-3">
      <div className="mb-2 flex items-center gap-2 text-[12.5px] font-medium text-ink">
        <Icon name="sparkle" size={14} className="text-accent" />
        <span>执行计划</span>
        <span className="text-faint">· {done}/{steps.length}</span>
        {blocked > 0 && <span className="text-accent">· {blocked} 项受阻</span>}
        {live && !allDone && (blocked === 0 || steps.some(s => s.status === "active")) ? (
          <span className="inline-flex items-center gap-1 text-accent">
            <span className="dot h-1.5 w-1.5 rounded-full bg-accent" /> 进行中
          </span>
        ) : allDone ? (
          <span className="text-ok">已完成</span>
        ) : null}
      </div>
      <ol className="space-y-1.5">
        {steps.map((s, i) => (
          <li key={i} className="flex items-start gap-2 text-[13px]">
            <span
              className={
                "mt-0.5 grid h-4 w-4 shrink-0 place-items-center rounded-full text-[9px] font-medium " +
                (s.status === "done"
                  ? "bg-ok text-white"
                  : s.status === "active"
                  ? "bg-accent text-white"
                  : s.status === "blocked"
                  ? "bg-accent text-white"
                  : "border border-faint text-faint")
              }
            >
              {s.status === "done" ? "✓" : s.status === "active" ? <span className="dot h-1.5 w-1.5 rounded-full bg-white" /> : s.status === "blocked" ? "!" : i + 1}
            </span>
            <span className={s.status === "done" ? "text-faint line-through" : s.status === "active" ? "text-ink font-medium" : s.status === "blocked" ? "text-accent" : "text-muted"}>
              {s.title}
              {s.status === "blocked" && <span className="ml-1 text-faint">· 受阻{s.reason ? `：${s.reason}` : ""}</span>}
            </span>
          </li>
        ))}
      </ol>
    </div>
  );
}

// extractCitations pulls unique external URLs out of an answer (both bare URLs
// and markdown-link targets), in first-appearance order — the basis for the
// "来源" footer that gives a research answer visible, checkable provenance.
function extractCitations(text: string): { url: string; host: string }[] {
  const urls = text.match(/https?:\/\/[^\s<>()\[\]"']+/gi) || [];
  const seen = new Set<string>();
  const out: { url: string; host: string }[] = [];
  for (let raw of urls) {
    raw = raw.replace(/[.,;:]+$/, ""); // trailing punctuation
    if (seen.has(raw)) continue;
    seen.add(raw);
    let host = raw;
    try { host = new URL(raw).hostname.replace(/^www\./, ""); } catch { /* keep raw */ }
    out.push({ url: raw, host });
  }
  return out;
}

// Citations is the collapsible 来源 footer under a research-style answer: it
// lists the unique cited domains as numbered chips, so the user can verify
// where a claim came from without scanning the prose for links.
function Citations({ text }: { text: string }) {
  const cites = extractCitations(text);
  const [open, setOpen] = useState(false);
  if (cites.length < 2) return null; // a single link reads fine inline
  return (
    <div className="mt-2.5">
      <button
        onClick={() => setOpen((o) => !o)}
        className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface px-2.5 py-1 text-[12px] text-muted transition hover:border-accent/40 hover:text-accent"
      >
        <Icon name="book" size={13} /> 来源 · {cites.length}
        <Icon name="chevron" size={12} className={"text-faint transition-transform " + (open ? "" : "-rotate-90")} />
      </button>
      {open && (
        <ol className="mt-2 space-y-1">
          {cites.map((c, i) => (
            <li key={c.url} className="flex items-start gap-2 text-[12.5px]">
              <span className="mt-0.5 grid h-4 w-4 shrink-0 place-items-center rounded-full bg-surface2 text-[10px] text-faint">{i + 1}</span>
              <a href={c.url} target="_blank" rel="noreferrer" className="min-w-0 truncate text-accent hover:underline" title={c.url}>
                <span className="text-ink">{c.host}</span>
                <span className="text-faint"> · {c.url.replace(/^https?:\/\//, "")}</span>
              </a>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}

function Assistant({ m, live, onRegenerate, onSchedule }: { m: Message; live?: boolean; onRegenerate?: () => void; onSchedule?: () => void }) {
  const scope = useContext(FileScopeCtx);
  const snapshots = useContext(DeliveryManifestCtx);
  const snapshotFor = useCallback((path: string) => !live && m.type === "chat" && m.meta?.run_id
    ? snapshots.find(snapshot => snapshot.conversation_id === scope.conversationID && snapshot.run_id === m.meta.run_id && snapshot.files.some(file => file.path === path))
    : undefined, [snapshots, scope.conversationID, m.meta?.run_id, m.type, live]);
  const resolveLink = useCallback((href: string) => {
    const path = workspaceLinkPath(href, scope);
    if (!path) return href;
    const snapshot = snapshotFor(path);
    return snapshot ? api.deliveryDownloadURL(scope.conversationID, snapshot.run_id, path) : fileApi.downloadURL(path, scope.conversationID);
  }, [scope.conversationID, scope.ownerEmail, snapshotFor]);
  const linkLabel = useCallback((href: string) => {
    const path = workspaceLinkPath(href, scope);
    if (!path) return undefined;
    const snapshot = snapshotFor(path);
    return snapshot ? `交付快照 · 版本 ${snapshot.version}` : "当前工作区 · 未关联交付快照";
  }, [scope.conversationID, scope.ownerEmail, snapshotFor]);
  return (
    <div className="group mb-7 flex gap-3.5">
      <div className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-full bg-accent text-white font-serif text-[13px] leading-none">
        O
      </div>
      <div className="min-w-0 flex-1 pt-0.5">
        <Markdown resolveLink={resolveLink} linkLabel={linkLabel}>{m.content ?? ""}</Markdown>
        {!live && <Citations text={m.content ?? ""} />}
        <div className="mt-1 flex gap-1 opacity-0 transition group-hover:opacity-100">
          <CopyButton text={m.content || ""} />
          {onRegenerate && <ActionButton label="重新生成" onClick={onRegenerate}><Icon name="refresh" size={13} /></ActionButton>}
          {onSchedule && <ActionButton label="把这轮设为定时任务" onClick={onSchedule}><Icon name="clock" size={13} /></ActionButton>}
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
      {done ? <Icon name="check" size={13} /> : <Icon name="copy" size={13} />}
    </ActionButton>
  );
}

const AGENT_ICON: Record<string, IconName> = { researcher: "search", writer: "rename", browser: "globe" };

// Display only captured evidence carried by this run's conversation stream.
// A shared administrator desktop is never embedded in an account's timeline.
function InlineComputer({ live, frames }: { live: boolean; frames: BrowserPayload[] }) {
  const [open, setOpen] = useState(true);
  const latest = frames.length ? frames[frames.length - 1] : undefined;
  return (
    <div className="mb-2 overflow-hidden rounded-xl border border-border bg-surface">
      <div className="flex items-center gap-1.5 border-b border-border bg-surface2 px-2.5 py-1.5">
        <span className={"h-1.5 w-1.5 rounded-full " + (live ? "animate-pulse bg-ok" : "bg-faint")} />
        <span className="text-[12px] text-muted">
          {live ? "本次运行 · 截图证据更新中" : "本次运行 · 截图证据"}{frames.length ? ` · ${frames.length} 帧` : ""}
        </span>
        <button onClick={() => setOpen((o) => !o)} aria-label={open ? "收起画面" : "展开画面"} className="ml-auto text-faint hover:text-accent">
          <Icon name="chevron" size={13} className={"transition-transform " + (open ? "" : "-rotate-90")} />
        </button>
      </div>
      {open && (
        latest?.data ? (
          <img src={"data:image/png;base64," + latest.data} alt="本次运行截图" className="block w-full" />
        ) : (
          <div className="px-3 py-6 text-center text-[12px] text-faint">本次运行未捕获画面</div>
        )
      )}
      <p className="px-3 py-2 text-[11px] text-faint">需要人工接管时，请由本地管理员单独打开 noVNC；此处仅展示运行证据。</p>
    </div>
  );
}

function Steps({ items, live }: { items: Message[]; live?: boolean }) {
  // Default-expanded while the run is live (the execution timeline IS the
  // product's differentiator); the user can still collapse, and it auto-folds
  // once the run settles — unless they pinned it open.
  const [pinned, setPinned] = useState<boolean | null>(null);
  const open = pinned ?? !!live;
  const browserShots = items
    .filter((m) => m.type === "browser")
    .map((m) => m.payload as BrowserPayload)
    .filter((p): p is BrowserPayload => !!p && !!p.data);
  // Frames belong to this step group; group() separates distinct run ids.
  const hasBrowser = items.some((m) => m.type === "browser");

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
        {live ? <Spinner /> : <Icon name="gear" size={13} className="text-faint" />}
        <span>
          {live ? "执行中" : open ? "收起" : "查看"} · {items.length} 步{lanes.size > 0 && ` · ${lanes.size} 个子 Agent`}
        </span>
      </button>
      {open && hasBrowser && (
        <div className="mt-2">
          <InlineComputer live={!!live} frames={browserShots} />
        </div>
      )}
      {open && (
        <div className="mt-2 border-l-2 border-border pl-4">
          {groupRuns(own).map((g) =>
            g.items.length > 1 ? (
              <CollapsedRun key={g.items[0].id} items={g.items} />
            ) : (
              <TimelineRow key={g.items[0].id} m={g.items[0]} />
            ),
          )}
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

// groupRuns collapses CONSECUTIVE successful calls of the same tool into one row.
// A long research task can fire twenty web_search calls in a row; twenty near
// identical lines bury the steps that actually matter. Anything that failed, and
// anything that is not a plain tool step, always stands on its own.
function groupRuns(items: Message[]): { key: string; items: Message[] }[] {
  const out: { key: string; items: Message[] }[] = [];
  for (const m of items) {
    const p = m.type === "tool" ? (m.payload as ToolPayload) : null;
    // errors and non-tool events are never folded away
    const receipt = p ? normalizeToolReceipt(p) : undefined;
    const key = p && !receipt?.error ? "tool:" + (p.tool || "") + ":" + (receipt?.browser?.label || "") : "solo:" + m.id;
    const last = out[out.length - 1];
    if (last && last.key === key) last.items.push(m);
    else out.push({ key, items: [m] });
  }
  return out;
}

// CollapsedRun renders a run of same-tool steps as one line ("搜索 · 5 次"),
// expandable to the individual steps.
function CollapsedRun({ items }: { items: Message[] }) {
  const [open, setOpen] = useState(false);
  const first = toolReceipt((items[0].payload as ToolPayload) || ({} as ToolPayload));
  const total = totalToolElapsedMs(items
    .filter((m) => m.type === "tool")
    .map((m) => (m.payload as ToolPayload) || ({ tool: "" } as ToolPayload)));
  if (open) {
    return (
      <>
        {items.map((m) => (
          <TimelineRow key={m.id} m={m} />
        ))}
        <button onClick={() => setOpen(false)} className="ml-1 py-0.5 text-[11.5px] text-faint hover:text-accent">收起这 {items.length} 步</button>
      </>
    );
  }
  return (
    <div className="relative flex items-start gap-2 py-0.5">
      <span className="-ml-[21px] mt-1.5 h-2 w-2 shrink-0 rounded-full bg-ok/70 ring-2 ring-surface" title={`${items.length} 步,全部成功`} />
      <button onClick={() => setOpen(true)} className="group flex items-center gap-1.5 text-left text-[13px]">
        <Icon name={first.icon} size={14} className="shrink-0 text-muted" />
        <span className="text-ink">{first.label.replace(/\s*“[^”]*”\s*$/, "")}</span>
        <span className="text-muted">· {items.length} 次</span>
        {total !== undefined && total > 400 && <span className="text-faint">· {(total / 1000).toFixed(1)}s</span>}
        <span className="text-faint opacity-0 transition group-hover:opacity-100">展开 ▾</span>
      </button>
    </div>
  );
}

// TimelineRow wraps one Step with a status node on the spine + elapsed time, so
// the steps read as a timeline ("图标 + 动作 + 耗时 + 状态") not a flat list.
function TimelineRow({ m }: { m: Message }) {
  const receipt = m.type === "tool" ? normalizeToolReceipt((m.payload as ToolPayload) || { tool: "" }) : undefined;
  const err = !!receipt?.error;
  const dur = receipt?.elapsedMs;
  return (
    <div className="relative flex items-start gap-2 py-0.5">
      <span
        className={"-ml-[21px] mt-1.5 h-2 w-2 shrink-0 rounded-full ring-2 ring-surface " + (err ? "bg-accent" : "bg-ok/70")}
        title={err ? "失败" : "完成"}
      />
      <div className="min-w-0 flex-1"><Step m={m} /></div>
      {dur !== undefined && dur > 400 && <span className="shrink-0 pt-0.5 text-[10px] text-faint">{(dur / 1000).toFixed(1)}s</span>}
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
        <Icon name="chevron" size={13} className={"ml-auto text-faint transition-transform " + (open ? "" : "-rotate-90")} />
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
  if (p.tool === "browser") {
    const receipt = normalizeToolReceipt(p).browser;
    return { icon: "globe", label: receipt?.label || "浏览器操作", detail: trunc(receipt?.detail, 70), file };
  }
  if (p.tool === "researcher" || p.tool === "writer") {
    return { icon: "users", label: `委派 ${p.tool}`, detail: trunc(s("task") || res, 64), file };
  }
  const r = base[p.tool || ""] || { icon: "wrench" as IconName, label: p.tool || "tool", detail: trunc(res, 70) };
  return { ...r, file };
}

// FileWriteActions gives every agent file write a trust affordance: "查看改动"
// jumps straight to the file's diff/history view, and "撤销" rolls the file back
// to the version saved right before this write (one-click undo). The undo button
// only appears once we confirm a prior version exists (i.e. the write actually
// OVERWROTE something) — a brand-new file has nothing to undo.
function FileWriteActions({ file: rawFile, onOpenDiff }: { file: string; onOpenDiff: () => void }) {
  const { conversationID, ownerEmail, readOnly } = useContext(FileScopeCtx);
  const file = normalizeWorkspacePath(rawFile, ownerEmail, conversationID) || "";
  const [hadPrior, setHadPrior] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [undone, setUndone] = useState(false);
  // Lazily check whether a backup exists for this file (cheap, one call).
  useEffect(() => {
    let on = true;
    setHadPrior(null); setUndone(false);
    if (!conversationID || !file || readOnly) return;
    fileApi.versions(file, conversationID).then((vs) => on && setHadPrior((vs?.length ?? 0) > 0)).catch(() => on && setHadPrior(false));
    return () => { on = false; };
  }, [file, conversationID, readOnly]);

  const undo = async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (busy || undone || readOnly || !conversationID || !file) return;
    const ok = await confirmDialog({
      title: `撤销对 ${file} 的改动?`,
      body: "会把文件恢复到这次写入之前的内容。此操作本身也可在版本历史里再次撤销。",
      confirmText: "撤销改动",
      danger: true,
    });
    if (!ok) return;
    setBusy(true);
    try {
      const vs = await fileApi.versions(file, conversationID);
      if (!vs?.length) { toastError("没有可恢复的历史版本"); return; }
      await fileApi.restore(file, vs[0].ts, conversationID); // newest backup = the pre-write state
      setUndone(true);
      toast(`已撤销 ${file} 的改动`);
    } catch {
      toastError("撤销失败");
    } finally {
      setBusy(false);
    }
  };

  if (readOnly || !conversationID || !file) return null;
  return (
    <span className="ml-1.5 inline-flex items-center gap-1.5 align-middle">
      <button onClick={(e) => { e.stopPropagation(); onOpenDiff(); }} className="text-[12px] text-faint hover:text-accent" title="查看这次写入的改动">查看改动</button>
      {hadPrior && (
        <button onClick={undo} disabled={busy || undone} className="text-[12px] text-faint hover:text-accent disabled:opacity-50" title="恢复到写入前的内容">
          {undone ? "已撤销" : busy ? "撤销中…" : "撤销"}
        </button>
      )}
    </span>
  );
}

function Step({ m }: { m: Message }) {
  const openFile = useContext(OpenFileCtx);
  const [shotBroken, setShotBroken] = useState(false);
  if (m.type === "tool") {
    const raw = (m.payload as ToolPayload) || ({} as ToolPayload);
    const p = { ...raw, error: executionError(raw) };
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
        {!p.error && p.tool === "file_write" && r.file && <FileWriteActions file={r.file} onOpenDiff={() => openFile(r.file!, { history: true })} />}
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
        {p?.data && !shotBroken ? (
          <img
            src={"data:image/png;base64," + p.data}
            alt="浏览器截图"
            onError={() => setShotBroken(true)}
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
function ConfirmCard({ m, active, onResumed }: { m: Message; active: boolean; onResumed?: (cid: string) => void }) {
  const p = m.payload as ConfirmPayload;
  const [done, setDone] = useState<"" | "once" | "always" | "reject">("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const decide = async (approve: boolean, always: boolean) => {
    if (!active || done || submitting) return;
    setSubmitting(true);
    setError("");
    try {
      // meta carries the conversation this pause belongs to, which the server
      // needs to resume the checkpointed run.
      const cid = m.meta?.conversation_id || "";
      const res = await chatApi.confirm(p.id, approve, always, cid);
      if (!res?.resolved) throw new Error("服务没有确认这次操作");
      setDone(approve ? (always ? "always" : "once") : "reject");
      // The backend resumed a checkpointed run, so its SSE is a NEW stream this
      // client is not listening to — re-attach or the answer never arrives.
      if (res?.resumed && cid) onResumed?.(cid);
    } catch (e) {
      setError(e instanceof Error ? e.message : "确认失败，请重试");
    } finally { setSubmitting(false); }
  };
  const doneLabel =
    done === "always" ? "✅ 本会话始终允许 · 继续执行" : done === "once" ? "✅ 已允许一次 · 继续执行" : "🚫 已拒绝,跳过该操作";
  return (
    <div className="mb-6 ml-[42px] rounded-xl border border-accent/40 bg-accentsoft/40 p-3.5">
      <div className="mb-1 flex items-center gap-1.5 text-[13px] font-medium text-ink">
        <Icon name="shield" size={14} className="text-accent" /> {active && !done ? "需要你确认" : "操作确认记录"} · {TOOL_LABEL[p.tool] || p.tool}
      </div>
      <div className="mb-2.5 break-words rounded-lg bg-surface/70 px-2.5 py-1.5 font-mono text-[12px] text-muted">{p.summary}</div>
      {done ? (
        <div className="text-[12.5px] text-faint">{doneLabel}</div>
      ) : !active ? (
        <div className="text-[12.5px] text-faint">此确认已不再待处理</div>
      ) : (
        <div className="flex flex-wrap gap-2">
          <button disabled={submitting} onClick={() => decide(true, false)} className="rounded-lg bg-accent px-3 py-1.5 text-[12.5px] text-white hover:opacity-90 disabled:opacity-50">{submitting ? "处理中…" : "允许一次"}</button>
          <button disabled={submitting} onClick={() => decide(true, true)} className="rounded-lg border border-accent/40 bg-surface px-3 py-1.5 text-[12.5px] text-ink hover:bg-accentsoft/60 disabled:opacity-50" title={`本会话内不再询问“${TOOL_LABEL[p.tool] || p.tool}”`}>本会话始终允许</button>
          <button disabled={submitting} onClick={() => decide(false, false)} className="rounded-lg border border-border bg-surface px-3 py-1.5 text-[12.5px] text-muted hover:border-accent/40 disabled:opacity-50">拒绝</button>
        </div>
      )}
      {active && error && <p role="alert" className="mt-2 text-[12px] text-accent">{error}</p>}
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
      <div className="flex gap-1" role="status">
        <span className="dot h-1.5 w-1.5 rounded-full bg-faint" />
        <span className="dot h-1.5 w-1.5 rounded-full bg-faint" />
        <span className="dot h-1.5 w-1.5 rounded-full bg-faint" />
        <span className="sr-only">正在思考…</span>
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
        <Icon name="chevron" size={11} className={"transition-transform " + (open ? "" : "-rotate-90")} />
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
// style); clicking one sends it. Generated automatically after completion and
// cached by owner, conversation, run and the answered model revision.


function trunc(s: string | undefined, n: number) {
  if (!s) return "";
  return s.length > n ? s.slice(0, n) + "…" : s;
}

// stripCard removes the embedded <weather-card> JSON block from a tool result
// so the collapsible step list shows only the human-readable text.
function stripCard(s: string): string {
  return s.replace(/\n?<weather-card>[\s\S]*?<\/weather-card>/g, "").trim();
}
