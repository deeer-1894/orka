import { useCallback, useEffect, useState } from "react";
import { api, auth, setOnUnauthorized } from "./api";
import { useChatStream } from "./hooks/useChatStream";
import { Login } from "./components/Login";
import { Sidebar } from "./components/Sidebar";
import { Thread } from "./components/Thread";
import { Composer } from "./components/Composer";
import { ArtifactDrawer } from "./components/ArtifactDrawer";
import type { Conversation, Message, OwnerInfo, TaskMeta } from "./types";

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
  const [tasks, setTasks] = useState<TaskMeta[]>([]);
  const [owners, setOwners] = useState<Record<string, OwnerInfo>>({});

  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerTab, setDrawerTab] = useState<Tab>("browser");

  const { messages, status, run, kill, setMessages } = useChatStream();

  const refreshTasks = useCallback(() => {
    api.getTasks().then((r) => {
      setTasks(r.tasks || []);
      setOwners(r.owners || {});
    }).catch(() => {});
  }, []);

  const refreshConversations = useCallback(() => {
    api.listConversations().then((c) => setConversations(c || [])).catch(() => {});
  }, []);

  // load conversation list + tasks on mount (persists across refresh)
  useEffect(() => {
    refreshConversations();
    refreshTasks();
  }, [refreshConversations, refreshTasks]);

  const newConversation = useCallback(async () => {
    const c = await api.createConversation("New chat");
    setConversations((cs) => [c, ...cs]);
    setActiveID(c.conversation_id);
    setMessages([]);
  }, [setMessages]);

  const selectConversation = useCallback(
    async (id: string) => {
      setActiveID(id);
      try {
        const rows = (await api.getMessages(id)) as (Message & { created_at?: number })[];
        setMessages(rows.map((r) => ({ ...r, ts: r.ts || r.created_at || Date.now() })).sort((a, b) => a.ts - b.ts));
      } catch {
        setMessages([]);
      }
    },
    [setMessages],
  );

  const ensureConversation = useCallback(async (): Promise<string> => {
    if (activeID) return activeID;
    const c = await api.createConversation("New chat");
    setConversations((cs) => [c, ...cs]);
    setActiveID(c.conversation_id);
    return c.conversation_id;
  }, [activeID]);

  const onSend = useCallback(
    async (msg: string) => {
      const id = await ensureConversation();
      // empty enabledTools => backend offers ALL tools; the model auto-selects
      await run({ message: msg, conversationID: id, userEmail: user.email, enabledTools: [] });
      refreshTasks();
      refreshConversations(); // pick up the auto-generated title
    },
    [ensureConversation, run, user.email, refreshTasks, refreshConversations],
  );

  const onRename = useCallback(async (id: string, title: string) => {
    await api.renameConversation(id, title);
    setConversations((cs) => cs.map((c) => (c.conversation_id === id ? { ...c, title } : c)));
  }, []);

  const onDelete = useCallback(
    async (id: string) => {
      await api.deleteConversation(id);
      setConversations((cs) => cs.filter((c) => c.conversation_id !== id));
      if (activeID === id) {
        setActiveID("");
        setMessages([]);
      }
    },
    [activeID, setMessages],
  );

  const onResume = useCallback(
    async (resumeKey: string, answer: string) => {
      await run({ message: answer, conversationID: activeID, userEmail: user.email, enabledTools: [], resumeKey });
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
          <span className="text-[12px] text-faint">deepseek-v4-flash</span>
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

        <Thread messages={messages} status={status} onResume={onResume} onOpenViewport={openViewport} />
        <Composer status={status} onSend={onSend} onKill={kill} />
      </main>

      <ArtifactDrawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        tab={drawerTab}
        setTab={setDrawerTab}
        messages={messages}
        email={user.email}
        tasks={tasks}
        owners={owners}
      />
    </div>
  );
}
