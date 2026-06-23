import { useCallback, useEffect, useRef, useState } from "react";
import { api, auth, setOnUnauthorized } from "./api";
import { useChatStreams } from "./hooks/useChatStream";
import { Login } from "./components/Login";
import { Sidebar } from "./components/Sidebar";
import { Thread } from "./components/Thread";
import { Composer } from "./components/Composer";
import { ArtifactDrawer } from "./components/ArtifactDrawer";
import { ShareDialog } from "./components/ShareDialog";
import { CommandPalette, type Command } from "./components/CommandPalette";
import { PublicArtifactPage, ArtifactBanner } from "./components/Artifacts";
import { useOverlay } from "./lib/useOverlay";
import { Toaster, toast } from "./lib/toast";
import { useTheme } from "./lib/theme";
import { useResource, refreshResource } from "./lib/useResource";
import { loadTools, saveTools } from "./lib/toolGroups";
import type { Conversation, Message, Notification } from "./types";

type Tab = "overview" | "artifacts" | "computer" | "files" | "runs" | "tasks" | "flows" | "integrations" | "metrics";

// Tools whose output the user watches in the 文件 face.
const FILE_TOOLS = new Set([
  "file_write", "file_read", "doc_export", "doc_read", "chart", "qrcode",
  "csv_to_json", "csv_to_xlsx", "xlsx_to_csv", "csv_join", "sql_query", "pdf_extract", "slides",
]);

// liveTab derives where the agent is working RIGHT NOW from the latest event, so
// the drawer can follow it (Live Focus). Null when idle.
function liveTabFromMessages(messages: Message[], streaming: boolean): Tab | null {
  if (!streaming) return null;
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m.type === "browser") return "computer";
    if (m.type === "tool") {
      const tool = (m.payload as { tool?: string })?.tool || "";
      if (tool === "run_agent") return "computer";
      if (tool === "artifact_publish" || tool === "artifact_get") return "artifacts";
      if (FILE_TOOLS.has(tool)) return "files";
      return null; // some other tool → no specific focus
    }
  }
  return null;
}

interface ModelOption { version: string; label: string; hint: string }
// Fallback until /models resolves (keeps the picker non-empty on first paint).
const MODELS_FALLBACK: ModelOption[] = [
  { version: "", label: "主模型", hint: "更强" },
  { version: "mini", label: "mini", hint: "更快 · 更省" },
];

export default function App() {
  // Public artifact page: /a/<slug>?t=<token> renders standalone, no login.
  const pubMatch = typeof window !== "undefined" && window.location.pathname.match(/^\/a\/([^/]+)$/);
  if (pubMatch) {
    const token = new URLSearchParams(window.location.search).get("t") || "";
    return (
      <>
        <PublicArtifactPage slug={decodeURIComponent(pubMatch[1])} token={token} />
        <Toaster />
      </>
    );
  }

  const [user, setUser] = useState<{ email: string; name: string } | null>(null);
  const [authReady, setAuthReady] = useState(false);

  // a 401 from any request → drop back to the login screen (no page reload)
  useEffect(() => {
    setOnUnauthorized(() => setUser(null));
  }, []);

  // restore session on load
  useEffect(() => {
    if (!auth.token()) {
      setAuthReady(true);
      return;
    }
    api
      .me()
      .then((u) => setUser({ email: u.email, name: u.name }))
      .catch(() => auth.clear())
      .finally(() => setAuthReady(true));
  }, []);

  return (
    <>
      {!authReady ? (
        <div className="h-screen" />
      ) : !user ? (
        <Login onAuthed={(s) => setUser({ email: s.email, name: s.name })} />
      ) : (
        <Workbench user={user} onSignOut={() => { auth.clear(); setUser(null); }} />
      )}
      <Toaster />
    </>
  );
}

function Workbench({
  user,
  onSignOut,
}: {
  user: { email: string; name: string };
  onSignOut: () => void;
}) {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [shared, setShared] = useState<Conversation[]>([]); // conversations others shared with me
  const [shareFor, setShareFor] = useState<Conversation | null>(null); // open share dialog
  const [drawerArtifact, setDrawerArtifact] = useState<string | null>(null); // artifact to open inline in the drawer
  const openArtifactInDrawer = useCallback((id: string) => { setDrawerArtifact(id); setDrawerOpen(true); setDrawerTab("artifacts"); }, []);
  const [activeID, setActiveID] = useState("");

  // Sidebar starts open on desktop, closed on narrow screens (where it overlays).
  const [sidebarOpen, setSidebarOpen] = useState(() =>
    typeof window === "undefined" ? true : window.innerWidth >= 768,
  );
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerTab, setDrawerTab] = useState<Tab>("overview");
  const [computerView, setComputerView] = useState<"terminal" | "browser">("terminal");
  // Shared metrics resource (also feeds the 指标 panel) — one poll, paused when hidden.
  const metricsRes = useResource("metrics", api.metrics, { interval: 4000 });
  const totalTokens = metricsRes?.total_tokens ?? 0;
  const [version, setVersion] = useState(""); // selected model version ("" main, "mini")
  const [theme, toggleTheme] = useTheme();
  // Per-conversation enabled tool groups (empty = all tools, the default).
  const [toolGroups, setToolGroups] = useState<Set<string>>(() => loadTools(""));
  const [models, setModels] = useState<ModelOption[]>(MODELS_FALLBACK);
  const [scheduleFor, setScheduleFor] = useState<string | null>(null); // prompt to schedule
  const [activeSkill, setActiveSkill] = useState<string | null>(null); // locked skill mode
  // Gate side-effecting tools (terminal/browser/network/code) behind approval.
  const [confirmRisky, setConfirmRisky] = useState(() => localStorage.getItem("orka.confirmRisky") !== "0");
  const toggleConfirm = useCallback(() => setConfirmRisky((v) => { localStorage.setItem("orka.confirmRisky", v ? "0" : "1"); return !v; }), []);

  useEffect(() => {
    api.models().then((m) => m.length && setModels(m)).catch(() => {});
  }, []);
  // conversation_ids that have a scheduled (cron) task → marked 🔁 in the sidebar.
  const [scheduledIds, setScheduledIds] = useState<Set<string>>(new Set());

  const { run, kill, setConvMessages, messagesOf, statusOf, runningIds } = useChatStreams();
  const messages = messagesOf(activeID);
  const status = statusOf(activeID);
  // Where the agent is working right now → the drawer follows it (Live Focus).
  const liveTab = liveTabFromMessages(messages, status === "streaming");
  // conversations whose history we've already loaded (or that have a live run),
  // so switching back never clobbers an in-flight stream with stale history.
  const seen = useRef<Set<string>>(new Set());

  const [cmdOpen, setCmdOpen] = useState(false);

  // Global keyboard shortcuts (mirrors what ChatGPT/Claude/Cursor offer):
  //   ⌘/Ctrl+K  → open the command palette
  //   Esc       → stop the running task (when one is streaming)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setCmdOpen((o) => !o);
      } else if (e.key === "Escape" && statusOf(activeID) === "streaming") {
        const el = document.activeElement;
        // don't hijack Esc while typing in an input (it closes menus there)
        if (!el || (el.tagName !== "INPUT" && el.tagName !== "TEXTAREA")) kill(activeID);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [activeID, statusOf, kill]);

  // Pull tasks to learn which conversations are scheduled (for the sidebar 🔁).
  const refreshTasks = useCallback(() => {
    api
      .getTasks()
      .then((r) => {
        const ids = new Set<string>();
        for (const t of r.tasks || []) {
          if (t.cron_status === "on" && t.conversation_id) ids.add(t.conversation_id);
        }
        setScheduledIds(ids);
      })
      .catch(() => {});
  }, []);

  const refreshConversations = useCallback(() => {
    api.listConversations().then((c) => setConversations(c || [])).catch(() => {});
    api.sharedWithMe().then((c) => setShared(c || [])).catch(() => {});
  }, []);

  // load conversation list + tasks on mount (persists across refresh)
  useEffect(() => {
    refreshConversations();
    refreshTasks();
  }, [refreshConversations, refreshTasks]);

  // The active conversation may be one I own or one shared with me; resolve it
  // and my role so the composer can go read-only for viewers.
  const activeConv = conversations.find((c) => c.conversation_id === activeID) || shared.find((c) => c.conversation_id === activeID);
  const isShared = !!activeConv?.owner_email && activeConv.owner_email !== user.email;
  const readOnly = isShared && !(activeConv?.shares ?? []).some((s) => s.email === user.email && s.role === "editor");

  // "New chat" just resets to the empty state — the server conversation is
  // created lazily on the first send (ensureConversation), so clicking around
  // never accumulates empty "New chat" placeholders.
  const newConversation = useCallback(() => setActiveID(""), []);

  const onPrune = useCallback(async () => {
    await api.pruneConversations().catch(() => {});
    refreshConversations();
  }, [refreshConversations]);

  const selectConversation = useCallback(
    async (id: string) => {
      setActiveID(id);
      if (seen.current.has(id)) return; // already loaded or has a live stream
      seen.current.add(id);
      try {
        const rows = (await api.getMessages(id)) as (Message & { created_at?: number })[];
        setConvMessages(id, rows.map((r) => ({ ...r, ts: r.ts || r.created_at || Date.now() })).sort((a, b) => a.ts - b.ts));
      } catch {
        setConvMessages(id, []);
      }
    },
    [setConvMessages],
  );

  // Load the saved tool-group selection whenever the active conversation changes.
  useEffect(() => {
    setToolGroups(loadTools(activeID));
  }, [activeID]);

  // Persist a new tool selection (group ids and/or individual tool names) for
  // the active conversation. Replaces the old per-group toggle so the picker can
  // express "this group minus one tool".
  const setTools = useCallback(
    (next: Set<string>) => {
      setToolGroups(next);
      saveTools(activeID, next);
    },
    [activeID],
  );

  const ensureConversation = useCallback(async (): Promise<string> => {
    if (activeID) return activeID;
    const c = await api.createConversation("New chat");
    setConversations((cs) => [c, ...cs]);
    setActiveID(c.conversation_id);
    return c.conversation_id;
  }, [activeID]);

  const lastMsgRef = useRef("");
  const onSend = useCallback(
    async (msg: string, fileIDs: string[] = []) => {
      lastMsgRef.current = msg;
      const id = await ensureConversation();
      seen.current.add(id);
      // carry a new chat's tool selection onto its freshly-created conversation id
      if (id && toolGroups.size) saveTools(id, toolGroups);
      const enabledTools = toolGroups.size ? [...toolGroups] : [];
      // fire-and-forget: do NOT await, so other conversations stay interactive
      // while this one streams. The backend runs each conversation concurrently.
      run({ message: msg, conversationID: id, userEmail: user.email, enabledTools, selectedVersion: version, activeSkill: activeSkill ?? "", fileIDs, confirmRisky }).then(() => {
        refreshConversations(); // pick up the auto-generated title
      });
      refreshTasks();
    },
    [ensureConversation, run, user.email, refreshTasks, refreshConversations, version, toolGroups, activeSkill, confirmRisky],
  );

  // Re-send the last user message after a failure (network drop, sandbox down…).
  const onRetry = useCallback(() => {
    if (lastMsgRef.current) onSend(lastMsgRef.current);
  }, [onSend]);

  const onRename = useCallback(async (id: string, title: string) => {
    await api.renameConversation(id, title);
    setConversations((cs) => cs.map((c) => (c.conversation_id === id ? { ...c, title } : c)));
  }, []);

  const onDelete = useCallback(
    async (id: string) => {
      await api.deleteConversation(id);
      setConversations((cs) => cs.filter((c) => c.conversation_id !== id));
      seen.current.delete(id);
      if (activeID === id) setActiveID("");
    },
    [activeID],
  );

  const onResume = useCallback(
    async (resumeKey: string, answer: string) => {
      run({ message: answer, conversationID: activeID, userEmail: user.email, enabledTools: [], resumeKey, selectedVersion: version });
      refreshTasks();
    },
    [run, activeID, user.email, refreshTasks, version],
  );

  // Jump from a task (Tasks panel) to its originating conversation.
  const onJumpToConversation = useCallback(
    (cid: string) => {
      if (!cid) return;
      selectConversation(cid);
      setDrawerOpen(false);
    },
    [selectConversation],
  );

  const openViewport = useCallback(() => {
    setComputerView("browser");
    setDrawerTab("computer");
    setDrawerOpen(true);
  }, []);

  // ⌘K command palette: collapse the app's scattered actions into one entry.
  const openPanel = (t: Tab) => { setDrawerTab(t); setDrawerOpen(true); };
  const commands: Command[] = [
    { id: "new", group: "操作", icon: "✚", label: "新建会话", hint: "New chat", run: newConversation },
    { id: "share", group: "操作", icon: "⤴", label: "分享当前会话", run: () => activeConv && setShareFor(conversations.find((c) => c.conversation_id === activeID) || null) },
    { id: "schedule", group: "操作", icon: "⏰", label: "把上一条设为定时任务", run: () => lastMsgRef.current && setScheduleFor(lastMsgRef.current) },
    { id: "theme", group: "设置", icon: theme === "dark" ? "☀" : "☾", label: theme === "dark" ? "切换到亮色" : "切换到暗色", run: toggleTheme },
    { id: "confirm", group: "设置", icon: "🛡", label: confirmRisky ? "关闭高危操作确认" : "开启高危操作确认", run: toggleConfirm },
    ...models.map((m): Command => ({ id: "model:" + m.version, group: "切换模型", icon: "◆", label: m.label, hint: m.hint, keywords: m.version, run: () => setVersion(m.version) })),
    ...([
      ["overview", "概览"], ["artifacts", "页面 Artifacts"], ["computer", "电脑"], ["files", "文件"],
      ["runs", "运行历史"], ["flows", "流程 / 工作流"], ["tasks", "定时任务"], ["integrations", "集成"], ["metrics", "指标"],
    ] as [Tab, string][]).map(([t, label]): Command => ({ id: "panel:" + t, group: "打开面板", icon: "▸", label, run: () => openPanel(t) })),
    ...conversations.slice(0, 60).map((c): Command => ({ id: "conv:" + c.conversation_id, group: "跳转会话", icon: "💬", label: c.title || "未命名会话", keywords: c.title, run: () => selectConversation(c.conversation_id) })),
  ];

  return (
    <div className="relative flex h-screen">
      {cmdOpen && <CommandPalette commands={commands} onClose={() => setCmdOpen(false)} />}
      {/* mobile backdrop: tapping it closes whichever overlay is open */}
      {(sidebarOpen || drawerOpen) && (
        <div
          className="fixed inset-0 z-30 bg-black/25 md:hidden"
          onClick={() => {
            setSidebarOpen(false);
            setDrawerOpen(false);
          }}
          aria-hidden="true"
        />
      )}
      <Sidebar
        open={sidebarOpen}
        onSelectClose={() => window.innerWidth < 768 && setSidebarOpen(false)}
        conversations={conversations}
        shared={shared}
        activeID={activeID}
        runningIds={runningIds}
        scheduledIds={scheduledIds}
        onSelect={selectConversation}
        onNew={newConversation}
        onRename={onRename}
        onDelete={onDelete}
        onPrune={onPrune}
        onShare={(id) => setShareFor(conversations.find((c) => c.conversation_id === id) || null)}
        name={user.name}
        email={user.email}
        onSignOut={onSignOut}
      />

      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 items-center gap-3 px-4">
          <button
            onClick={() => setSidebarOpen((o) => !o)}
            className="grid h-8 w-8 place-items-center rounded-lg text-muted hover:bg-surface2"
            title="Toggle sidebar"
            aria-label="切换侧栏"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
              <path d="M3 6h18M3 12h18M3 18h18" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            </svg>
          </button>
          {/* Show the active conversation (with live status) instead of a
              redundant brand — "Orka" already lives in the sidebar. */}
          <div className="flex min-w-0 items-center gap-1.5">
            {status === "streaming" && <span className="h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-ok" title="运行中" />}
            <span className="max-w-[40vw] truncate text-[15px] text-ink md:max-w-[260px]" title={activeConv?.title || "新会话"}>
              {activeConv?.title || "新会话"}
            </span>
            {isShared && <span className="shrink-0 text-[11px] text-faint" title={`由 ${activeConv?.owner_email} 分享`}>· 共享</span>}
          </div>
          <ModelSelect value={version} onChange={setVersion} models={models} />
          {totalTokens > 0 && (
            <span className="text-[11px] text-faint" title="本进程累计 token 用量">
              🪙 {totalTokens >= 1000 ? (totalTokens / 1000).toFixed(1) + "k" : totalTokens} tokens
            </span>
          )}
          <NotificationBell onJump={onJumpToConversation} />
          <button
            onClick={toggleTheme}
            className="grid h-8 w-8 place-items-center rounded-lg text-muted hover:bg-surface2"
            title={theme === "dark" ? "切换到亮色" : "切换到暗色"}
            aria-label={theme === "dark" ? "切换到亮色模式" : "切换到暗色模式"}
          >
            {theme === "dark" ? "☀️" : "🌙"}
          </button>
          <button
            onClick={() => setDrawerOpen((o) => !o)}
            aria-label="切换工件面板"
            aria-pressed={drawerOpen}
            className={
              "rounded-lg border px-3 py-1.5 text-[13px] transition " +
              (drawerOpen ? "border-accent/40 bg-accentsoft text-accent" : "border-border text-muted hover:bg-surface2")
            }
          >
            Artifacts
          </button>
        </header>

        <Thread messages={messages} status={status} onResume={onResume} onOpenViewport={openViewport} onPick={onSend} onRetry={onRetry} onSchedule={setScheduleFor} fileConv={isShared ? activeID : undefined} />
        {activeID && <div className="px-5"><ArtifactBanner conversationId={activeID} onOpen={openArtifactInDrawer} /></div>}
        {readOnly ? (
          <div className="mx-auto mb-4 w-full max-w-3xl px-5">
            <div className="rounded-xl border border-border bg-surface2/50 px-4 py-3 text-center text-[13px] text-muted">
              👁 只读会话 · 由 {activeConv?.owner_email} 分享 · 你无法发送消息
            </div>
          </div>
        ) : (
          <Composer status={status} onSend={onSend} onKill={() => kill(activeID)} enabledTools={toolGroups} onSetTools={setTools} activeSkill={activeSkill} onPickSkill={setActiveSkill} confirmRisky={confirmRisky} onToggleConfirm={toggleConfirm} />
        )}
      </main>

      <ArtifactDrawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        tab={drawerTab}
        setTab={setDrawerTab}
        liveTab={liveTab}
        computerView={computerView}
        setComputerView={setComputerView}
        messages={messages}
        email={user.email}
        onJumpToConversation={onJumpToConversation}
        focusArtifact={drawerArtifact}
        onClearArtifact={() => setDrawerArtifact(null)}
      />

      {scheduleFor !== null && (
        <ScheduleDialog
          prompt={scheduleFor}
          onClose={() => setScheduleFor(null)}
          onConfirm={async (sec, retry) => {
            await api.scheduleTask(scheduleFor, sec, scheduleFor.slice(0, 24), activeID, retry).catch(() => {});
            setScheduleFor(null);
            refreshTasks();
            toast("已设为定时任务", "success");
          }}
        />
      )}

      {shareFor && (
        <ShareDialog
          conv={shareFor}
          onClose={() => setShareFor(null)}
          onChanged={(shares) => {
            setConversations((cs) => cs.map((c) => (c.conversation_id === shareFor.conversation_id ? { ...c, shares } : c)));
            setShareFor((f) => (f ? { ...f, shares } : f));
          }}
        />
      )}

    </div>
  );
}

const INTERVALS = [
  { label: "每 10 分钟", sec: 600 },
  { label: "每小时", sec: 3600 },
  { label: "每天", sec: 86400 },
];

// ScheduleDialog turns a conversation turn into a recurring task (with the prompt
// prefilled). Supports the presets plus a custom minutes value (backend min 30s).
function ScheduleDialog({ prompt, onClose, onConfirm }: { prompt: string; onClose: () => void; onConfirm: (sec: number, retry: number) => void }) {
  const [sec, setSec] = useState(3600);
  const [customMin, setCustomMin] = useState("");
  const [retry, setRetry] = useState(0);
  const effective = customMin ? Math.max(1, Math.round(Number(customMin))) * 60 : sec;
  const valid = effective >= 30;
  useOverlay(onClose);
  return (
    <div className="overlay-in fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
      <div className="pop-in w-full max-w-md rounded-2xl border border-border bg-surface p-5 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <h2 className="font-serif text-[18px] text-ink">设为定时任务</h2>
        <p className="mt-1 line-clamp-2 text-[13px] text-muted">“{prompt}”</p>
        <div className="mt-3 flex flex-wrap gap-1.5">
          {INTERVALS.map((i) => (
            <button
              key={i.sec}
              onClick={() => { setSec(i.sec); setCustomMin(""); }}
              className={
                "rounded-lg border px-3 py-1.5 text-[13px] transition " +
                (!customMin && sec === i.sec ? "border-accent/40 bg-accentsoft text-accent" : "border-border text-muted hover:bg-surface2")
              }
            >
              {i.label}
            </button>
          ))}
          <div className="flex items-center gap-1 rounded-lg border border-border px-2 py-1 text-[13px]">
            <span className="text-faint">每</span>
            <input
              value={customMin}
              onChange={(e) => setCustomMin(e.target.value.replace(/[^\d]/g, ""))}
              placeholder="—"
              aria-label="自定义间隔（分钟）"
              className="w-12 bg-transparent text-center outline-none"
            />
            <span className="text-faint">分钟</span>
          </div>
        </div>
        <div className="mt-3 flex items-center gap-2 text-[13px] text-muted">
          <span>失败自动重试</span>
          <input
            value={retry}
            onChange={(e) => setRetry(Math.max(0, Math.min(5, Number(e.target.value.replace(/[^\d]/g, "")) || 0)))}
            aria-label="失败重试次数"
            className="w-12 rounded-lg border border-border bg-surface px-2 py-1 text-center outline-none focus:border-accent/50"
          />
          <span>次</span>
        </div>
        <div className="mt-4 flex justify-end gap-2">
          <button onClick={onClose} className="rounded-lg px-3 py-1.5 text-[13px] text-muted hover:bg-surface2">取消</button>
          <button
            onClick={() => valid && onConfirm(effective, retry)}
            disabled={!valid}
            className="rounded-lg bg-accent px-3.5 py-1.5 text-[13px] text-white disabled:opacity-40"
          >
            创建
          </button>
        </div>
        {!valid && <div className="mt-2 text-right text-[11px] text-accent">间隔需 ≥ 30 秒</div>}
      </div>
    </div>
  );
}

// ModelSelect is the header model picker; it sets the per-run `selected_version`
// the backend's modelFor() reads ("" → main model, "mini" → cheaper/faster).
// NotificationBell surfaces unattended-run failures (the alerting half of run
// history): a header bell with an unread badge + a dropdown that jumps to the
// failed run's conversation.
function NotificationBell({ onJump }: { onJump: (cid: string) => void }) {
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<Notification[]>([]);
  // Shared, hidden-paused poll for the unread badge.
  const notif = useResource("notifications", () => api.listNotifications(), { interval: 20000 });
  const unread = notif?.unread ?? 0;
  const load = () => api.listNotifications().then((r) => setItems(r.notifications || [])).catch(() => {});
  return (
    <div className="relative ml-auto">
      <button
        onClick={() => { setOpen((o) => !o); if (!open) load(); }}
        aria-label={"通知" + (unread > 0 ? `（${unread} 条未读）` : "")}
        className="relative grid h-8 w-8 place-items-center rounded-lg text-[16px] text-muted hover:bg-surface2"
      >
        🔔
        {unread > 0 && (
          <span className="absolute -right-0.5 -top-0.5 grid h-4 min-w-[16px] place-items-center rounded-full bg-accent px-1 text-[10px] text-white">{unread > 9 ? "9+" : unread}</span>
        )}
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-10" onClick={() => setOpen(false)} />
          <div className="absolute right-0 z-20 mt-1 w-80 rounded-xl border border-border bg-surface p-1.5 shadow-lg">
            <div className="flex items-center justify-between px-2 py-1">
              <span className="text-[11px] font-medium uppercase tracking-wide text-faint">通知</span>
              {items.length > 0 && <button onClick={() => api.readNotifications().then(() => { load(); refreshResource("notifications"); })} className="text-[11px] text-faint hover:text-accent">全部已读</button>}
            </div>
            {items.length === 0 && <div className="px-2 py-4 text-center text-[13px] text-faint">暂无通知</div>}
            <div className="max-h-[60vh] overflow-y-auto">
              {items.map((n) => (
                <button
                  key={n.notification_id}
                  onClick={() => { api.readNotifications(n.notification_id).then(() => { load(); refreshResource("notifications"); }); if (n.conversation_id) { onJump(n.conversation_id); setOpen(false); } }}
                  className={"flex w-full flex-col items-start gap-0.5 rounded-lg px-2 py-1.5 text-left hover:bg-surface2 " + (n.read ? "opacity-60" : "")}
                >
                  <span className="text-[13px] text-ink">
                    {!n.read && <span className="mr-1 inline-block h-1.5 w-1.5 rounded-full bg-accent align-middle" />}
                    {n.title}
                  </span>
                  <span className="line-clamp-2 text-[12px] text-muted">{n.body}</span>
                </button>
              ))}
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function ModelSelect({ value, onChange, models }: { value: string; onChange: (v: string) => void; models: ModelOption[] }) {
  const [open, setOpen] = useState(false);
  const cur = models.find((m) => m.version === value) || models[0];
  return (
    <div className="relative">
      <button
        onClick={() => setOpen((o) => !o)}
        aria-label="选择模型"
        aria-haspopup="listbox"
        aria-expanded={open}
        className="flex items-center gap-1 rounded-full bg-surface2 px-2 py-0.5 text-[11px] text-muted hover:text-ink"
      >
        {cur.label}
        <span className="text-faint">▾</span>
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-10" onClick={() => setOpen(false)} />
          <div className="absolute left-0 z-20 mt-1 w-56 rounded-xl border border-border bg-surface p-1 shadow-lg" role="listbox">
            {models.map((m) => (
              <button
                key={m.version}
                role="option"
                aria-selected={m.version === value}
                onClick={() => {
                  onChange(m.version);
                  setOpen(false);
                }}
                className={
                  "flex w-full items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[13px] " +
                  (m.version === value ? "bg-accentsoft text-accent" : "text-ink hover:bg-surface2")
                }
              >
                <span className="truncate">{m.label}</span>
                <span className="shrink-0 text-[11px] text-faint">{m.hint}</span>
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
