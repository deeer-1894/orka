import { useCallback, useEffect, useRef, useState } from "react";
import { api, auth, setOnUnauthorized } from "./api";
import { useChatStreams } from "./hooks/useChatStream";
import { Login } from "./components/Login";
import { Sidebar } from "./components/Sidebar";
import { Thread } from "./components/Thread";
import { Composer } from "./components/Composer";
import { ArtifactDrawer } from "./components/ArtifactDrawer";
import { Toaster, toast } from "./lib/toast";
import { useTheme } from "./lib/theme";
import { loadTools, saveTools } from "./lib/toolGroups";
import type { Conversation, Message } from "./types";

type Tab = "browser" | "files" | "runs" | "tasks" | "integrations" | "metrics";

interface ModelOption { version: string; label: string; hint: string }
// Fallback until /models resolves (keeps the picker non-empty on first paint).
const MODELS_FALLBACK: ModelOption[] = [
  { version: "", label: "主模型", hint: "更强" },
  { version: "mini", label: "mini", hint: "更快 · 更省" },
];

export default function App() {
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
  const [activeID, setActiveID] = useState("");

  // Sidebar starts open on desktop, closed on narrow screens (where it overlays).
  const [sidebarOpen, setSidebarOpen] = useState(() =>
    typeof window === "undefined" ? true : window.innerWidth >= 768,
  );
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerTab, setDrawerTab] = useState<Tab>("browser");
  const [totalTokens, setTotalTokens] = useState(0);
  const [version, setVersion] = useState(""); // selected model version ("" main, "mini")
  const [theme, toggleTheme] = useTheme();
  // Per-conversation enabled tool groups (empty = all tools, the default).
  const [toolGroups, setToolGroups] = useState<Set<string>>(() => loadTools(""));
  const [models, setModels] = useState<ModelOption[]>(MODELS_FALLBACK);
  const [scheduleFor, setScheduleFor] = useState<string | null>(null); // prompt to schedule
  const [activeSkill, setActiveSkill] = useState<string | null>(null); // locked skill mode

  useEffect(() => {
    api.models().then((m) => m.length && setModels(m)).catch(() => {});
  }, []);
  // conversation_ids that have a scheduled (cron) task → marked 🔁 in the sidebar.
  const [scheduledIds, setScheduledIds] = useState<Set<string>>(new Set());

  const { run, kill, setConvMessages, messagesOf, statusOf, runningIds } = useChatStreams();
  const messages = messagesOf(activeID);
  const status = statusOf(activeID);
  // conversations whose history we've already loaded (or that have a live run),
  // so switching back never clobbers an in-flight stream with stale history.
  const seen = useRef<Set<string>>(new Set());

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
  }, []);

  // load conversation list + tasks on mount (persists across refresh)
  useEffect(() => {
    refreshConversations();
    refreshTasks();
  }, [refreshConversations, refreshTasks]);

  // live token-usage chip in the header
  useEffect(() => {
    const t = () => api.metrics().then((s) => setTotalTokens(s.total_tokens || 0)).catch(() => {});
    t();
    const id = setInterval(t, 4000);
    return () => clearInterval(id);
  }, []);

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

  const toggleGroup = useCallback(
    (id: string) => {
      setToolGroups((prev) => {
        const next = new Set(prev);
        next.has(id) ? next.delete(id) : next.add(id);
        saveTools(activeID, next);
        return next;
      });
    },
    [activeID],
  );
  const clearGroups = useCallback(() => {
    setToolGroups(new Set());
    saveTools(activeID, new Set());
  }, [activeID]);

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
      run({ message: msg, conversationID: id, userEmail: user.email, enabledTools, selectedVersion: version, activeSkill: activeSkill ?? "", fileIDs }).then(() => {
        refreshConversations(); // pick up the auto-generated title
      });
      refreshTasks();
    },
    [ensureConversation, run, user.email, refreshTasks, refreshConversations, version, toolGroups, activeSkill],
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
    setDrawerTab("browser");
    setDrawerOpen(true);
  }, []);

  return (
    <div className="relative flex h-screen">
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
        activeID={activeID}
        runningIds={runningIds}
        scheduledIds={scheduledIds}
        onSelect={selectConversation}
        onNew={newConversation}
        onRename={onRename}
        onDelete={onDelete}
        onPrune={onPrune}
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
          <span className="font-serif text-[16px] text-ink">Orka</span>
          <ModelSelect value={version} onChange={setVersion} models={models} />
          {totalTokens > 0 && (
            <span className="text-[11px] text-faint" title="本进程累计 token 用量">
              🪙 {totalTokens >= 1000 ? (totalTokens / 1000).toFixed(1) + "k" : totalTokens} tokens
            </span>
          )}
          <button
            onClick={toggleTheme}
            className="ml-auto grid h-8 w-8 place-items-center rounded-lg text-muted hover:bg-surface2"
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

        <Thread messages={messages} status={status} onResume={onResume} onOpenViewport={openViewport} onPick={onSend} onRetry={onRetry} onSchedule={setScheduleFor} />
        <Composer status={status} onSend={onSend} onKill={() => kill(activeID)} enabledGroups={toolGroups} onToggleGroup={toggleGroup} onClearGroups={clearGroups} activeSkill={activeSkill} onPickSkill={setActiveSkill} />
      </main>

      <ArtifactDrawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        tab={drawerTab}
        setTab={setDrawerTab}
        messages={messages}
        email={user.email}
        onJumpToConversation={onJumpToConversation}
      />

      {scheduleFor !== null && (
        <ScheduleDialog
          prompt={scheduleFor}
          onClose={() => setScheduleFor(null)}
          onConfirm={async (sec) => {
            await api.scheduleTask(scheduleFor, sec, scheduleFor.slice(0, 24), activeID).catch(() => {});
            setScheduleFor(null);
            refreshTasks();
            toast("已设为定时任务", "success");
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
function ScheduleDialog({ prompt, onClose, onConfirm }: { prompt: string; onClose: () => void; onConfirm: (sec: number) => void }) {
  const [sec, setSec] = useState(3600);
  const [customMin, setCustomMin] = useState("");
  const effective = customMin ? Math.max(1, Math.round(Number(customMin))) * 60 : sec;
  const valid = effective >= 30;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
      <div className="w-full max-w-md rounded-2xl border border-border bg-surface p-5 shadow-xl" onClick={(e) => e.stopPropagation()}>
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
        <div className="mt-4 flex justify-end gap-2">
          <button onClick={onClose} className="rounded-lg px-3 py-1.5 text-[13px] text-muted hover:bg-surface2">取消</button>
          <button
            onClick={() => valid && onConfirm(effective)}
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
