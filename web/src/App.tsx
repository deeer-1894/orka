import { useCallback, useEffect, useState } from "react";
import { api } from "./api";
import { useChatStream } from "./hooks/useChatStream";
import { StatusBar } from "./components/StatusBar";
import { LeftRail } from "./components/LeftRail";
import { EventStream } from "./components/EventStream";
import { Composer } from "./components/Composer";
import { Inspector } from "./components/Inspector";
import type { Conversation, Message, OwnerInfo, TaskMeta } from "./types";

const MODEL = "deepseek-v4-flash";
const RUN_MODE = "adk";

export default function App() {
  const [email, setEmail] = useState(
    () => localStorage.getItem("cavis.email") || "you@cavis.dev",
  );
  useEffect(() => localStorage.setItem("cavis.email", email), [email]);

  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeID, setActiveID] = useState("");
  const [tasks, setTasks] = useState<TaskMeta[]>([]);
  const [owners, setOwners] = useState<Record<string, OwnerInfo>>({});
  const [enabled, setEnabled] = useState<string[]>(["file"]);

  const { messages, status, run, kill, setMessages } = useChatStream();
  const trace = [...messages].reverse().find((m) => m.meta?.trace_id)?.meta.trace_id;

  const refreshTasks = useCallback(() => {
    api.getTasks().then((r) => {
      setTasks(r.tasks || []);
      setOwners(r.owners || {});
    }).catch(() => {});
  }, []);

  useEffect(() => {
    refreshTasks();
  }, [refreshTasks]);

  const newConversation = useCallback(async () => {
    const c = await api.createConversation("Session " + new Date().toLocaleTimeString());
    setConversations((cs) => [c, ...cs]);
    setActiveID(c.conversation_id);
    setMessages([]);
  }, [setMessages]);

  const selectConversation = useCallback(
    async (id: string) => {
      setActiveID(id);
      try {
        const rows = (await api.getMessages(id)) as (Message & { created_at?: number })[];
        setMessages(
          rows
            .map((r) => ({ ...r, ts: r.ts || r.created_at || Date.now() }))
            .sort((a, b) => a.ts - b.ts),
        );
      } catch {
        setMessages([]);
      }
    },
    [setMessages],
  );

  const ensureConversation = useCallback(async (): Promise<string> => {
    if (activeID) return activeID;
    const c = await api.createConversation("Session " + new Date().toLocaleTimeString());
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
      await run({
        message: answer,
        conversationID: activeID,
        userEmail: email,
        enabledTools: enabled,
        resumeKey,
      });
      refreshTasks();
    },
    [run, activeID, email, enabled, refreshTasks],
  );

  return (
    <div className="flex h-screen flex-col">
      <StatusBar status={status} model={MODEL} runMode={RUN_MODE} trace={trace} />
      <div className="flex min-h-0 flex-1">
        <LeftRail
          conversations={conversations}
          activeID={activeID}
          onSelect={selectConversation}
          onNew={newConversation}
          tasks={tasks}
          owners={owners}
        />
        <main className="flex min-w-0 flex-1 flex-col">
          <EventStream messages={messages} status={status} onResume={onResume} />
          <Composer
            status={status}
            enabled={enabled}
            setEnabled={setEnabled}
            email={email}
            setEmail={setEmail}
            onSend={onSend}
            onKill={kill}
          />
        </main>
        <Inspector messages={messages} email={email} />
      </div>
    </div>
  );
}
