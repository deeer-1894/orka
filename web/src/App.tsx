import { useCallback, useEffect, useState } from "react";
import { api } from "./api";
import { useChatStream } from "./hooks/useChatStream";
import { Sidebar } from "./components/Sidebar";
import { Thread } from "./components/Thread";
import { Composer } from "./components/Composer";
import { ArtifactDrawer } from "./components/ArtifactDrawer";
import type { Conversation, Message, OwnerInfo, TaskMeta } from "./types";

type Tab = "browser" | "files" | "metrics" | "tasks";

export default function App() {
  const [email, setEmail] = useState(() => localStorage.getItem("cavis.email") || "you@cavis.dev");
  useEffect(() => localStorage.setItem("cavis.email", email), [email]);

  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeID, setActiveID] = useState("");
  const [tasks, setTasks] = useState<TaskMeta[]>([]);
  const [owners, setOwners] = useState<Record<string, OwnerInfo>>({});
  const [enabled, setEnabled] = useState<string[]>(["search", "file"]);

  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerTab, setDrawerTab] = useState<Tab>("browser");

  const { messages, status, run, kill, setMessages } = useChatStream();

  const refreshTasks = useCallback(() => {
    api
      .getTasks()
      .then((r) => {
        setTasks(r.tasks || []);
        setOwners(r.owners || {});
      })
      .catch(() => {});
  }, []);
  useEffect(() => refreshTasks(), [refreshTasks]);

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
      await run({ message: msg, conversationID: id, userEmail: email, enabledTools: enabled });
      refreshTasks();
    },
    [ensureConversation, run, email, enabled, refreshTasks],
  );

  const onResume = useCallback(
    async (resumeKey: string, answer: string) => {
      await run({ message: answer, conversationID: activeID, userEmail: email, enabledTools: enabled, resumeKey });
      refreshTasks();
    },
    [run, activeID, email, enabled, refreshTasks],
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
        email={email}
        setEmail={setEmail}
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
          <span className="font-serif text-[16px] text-ink">Cavis</span>
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
        <Composer status={status} enabled={enabled} setEnabled={setEnabled} onSend={onSend} onKill={kill} />
      </main>

      <ArtifactDrawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        tab={drawerTab}
        setTab={setDrawerTab}
        messages={messages}
        email={email}
        tasks={tasks}
        owners={owners}
      />
    </div>
  );
}
