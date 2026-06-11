import { useCallback, useEffect, useRef, useState } from "react";
import { api, auth, setOnUnauthorized } from "./api";
import { useChatStreams } from "./hooks/useChatStream";
import { Login } from "./components/Login";
import { Sidebar } from "./components/Sidebar";
import { Thread } from "./components/Thread";
import { Composer } from "./components/Composer";
import { ArtifactDrawer } from "./components/ArtifactDrawer";
import type { Conversation, Message } from "./types";

type Tab = "browser" | "files" | "metrics" | "tasks";

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

  if (!authReady) return <div className="h-screen" />;
  if (!user) return <Login onAuthed={(s) => setUser({ email: s.email, name: s.name })} />;
  return <Workbench user={user} onSignOut={() => { auth.clear(); setUser(null); }} />;
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

  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerTab, setDrawerTab] = useState<Tab>("browser");
  const [totalTokens, setTotalTokens] = useState(0);

  const { run, kill, setConvMessages, messagesOf, statusOf, runningIds } = useChatStreams();
  const messages = messagesOf(activeID);
  const status = statusOf(activeID);
  // conversations whose history we've already loaded (or that have a live run),
  // so switching back never clobbers an in-flight stream with stale history.
  const seen = useRef<Set<string>>(new Set());

  // Tasks now self-fetch inside the Tasks panel; keep a no-op so call sites stay simple.
  const refreshTasks = useCallback(() => {}, []);

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

  const newConversation = useCallback(async () => {
    const c = await api.createConversation("New chat");
    setConversations((cs) => [c, ...cs]);
    seen.current.add(c.conversation_id);
    setConvMessages(c.conversation_id, []);
    setActiveID(c.conversation_id);
  }, [setConvMessages]);

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

  const ensureConversation = useCallback(async (): Promise<string> => {
    if (activeID) return activeID;
    const c = await api.createConversation("New chat");
    setConversations((cs) => [c, ...cs]);
    setActiveID(c.conversation_id);
    return c.conversation_id;
  }, [activeID]);

  const lastMsgRef = useRef("");
  const onSend = useCallback(
    async (msg: string) => {
      lastMsgRef.current = msg;
      const id = await ensureConversation();
      seen.current.add(id);
      // fire-and-forget: do NOT await, so other conversations stay interactive
      // while this one streams. The backend runs each conversation concurrently.
      run({ message: msg, conversationID: id, userEmail: user.email, enabledTools: [] }).then(() => {
        refreshConversations(); // pick up the auto-generated title
      });
      refreshTasks();
    },
    [ensureConversation, run, user.email, refreshTasks, refreshConversations],
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
      run({ message: answer, conversationID: activeID, userEmail: user.email, enabledTools: [], resumeKey });
      refreshTasks();
    },
    [run, activeID, user.email, refreshTasks],
  );

  const openViewport = useCallback(() => {
    setDrawerTab("browser");
    setDrawerOpen(true);
  }, []);

  return (
    <div className="flex h-screen">
      <Sidebar
        open={sidebarOpen}
        conversations={conversations}
        activeID={activeID}
        runningIds={runningIds}
        onSelect={selectConversation}
        onNew={newConversation}
        onRename={onRename}
        onDelete={onDelete}
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
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none">
              <path d="M3 6h18M3 12h18M3 18h18" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            </svg>
          </button>
          <span className="font-serif text-[16px] text-ink">Orka</span>
          <span className="rounded-full bg-surface2 px-2 py-0.5 text-[11px] text-muted">deepseek-v4-flash</span>
          {totalTokens > 0 && (
            <span className="text-[11px] text-faint" title="本进程累计 token 用量">
              🪙 {totalTokens >= 1000 ? (totalTokens / 1000).toFixed(1) + "k" : totalTokens} tokens
            </span>
          )}
          <button
            onClick={() => setDrawerOpen((o) => !o)}
            className={
              "ml-auto rounded-lg border px-3 py-1.5 text-[13px] transition " +
              (drawerOpen ? "border-accent/40 bg-accentsoft text-accent" : "border-border text-muted hover:bg-surface2")
            }
          >
            Artifacts
          </button>
        </header>

        <Thread messages={messages} status={status} onResume={onResume} onOpenViewport={openViewport} onPick={onSend} onRetry={onRetry} />
        <Composer status={status} onSend={onSend} onKill={() => kill(activeID)} />
      </main>

      <ArtifactDrawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        tab={drawerTab}
        setTab={setDrawerTab}
        messages={messages}
        email={user.email}
      />
    </div>
  );
}
