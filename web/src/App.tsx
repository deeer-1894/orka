import { ActionChip } from './components/ActionChip';
import { useConversationDraft } from './hooks/useConversationDraft';
import { SessionRecoveryStore } from './lib/sessionRecovery';
import { useConversationSettings } from './hooks/useConversationSettings';
import { Suspense, lazy, useCallback, useSyncExternalStore, useEffect, useRef, useState } from "react";
import { api, auth, setOnUnauthorized } from "./api";
import { useChatStreams } from "./hooks/useChatStream";
import { useRunRecovery } from "./hooks/useRunRecovery";
import { useEventStream } from "./hooks/useEventStream";
import { ModelSettings } from "./components/ModelSettings";
import { Login } from "./components/Login";
import { Sidebar } from "./components/Sidebar";
import { Thread } from "./components/Thread";
import { Composer } from "./components/Composer";
const ArtifactDrawer = lazy(() => import("./components/ArtifactDrawer").then(m => ({ default: m.ArtifactDrawer })));
import { useRunActions } from "./hooks/useRunActions";
import { invalidateSessionFiles } from "./hooks/useFileRevision";
import { clearFollowUps } from "./components/FollowUps";
import { ShareDialog } from "./components/ShareDialog";
import { CommandPalette, type Command } from "./components/CommandPalette";
import { Icon } from "./components/Icon";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from "@/components/ui/dropdown-menu";
import { PublicArtifactPage, ArtifactBanner } from "./components/Artifacts";
import { useOverlay } from "./lib/useOverlay";
import { Toaster, toast } from "./lib/toast";
import { ConfirmHost } from "./lib/confirm";
import { useTheme } from "./lib/theme";
import { useResource, refreshResource, refreshResourcePrefix } from "./lib/useResource";
import type { Conversation, Message, Notification, Project } from "./types";

import type { WorkbenchTab as Tab } from "./lib/workbenchTabs";
import type { TaskDraft } from "./components/tasks/TaskEditor";
import { Onboarding, TRY_PROMPT, onboardingVisible } from "./components/Onboarding";
import { CHAT, TASKS, hashOf, viewFromHash, type View } from "./lib/view";
import { ProjectPage } from "./components/projects/ProjectPage";
import { MoveToProjectDialog } from "./components/projects/MoveToProjectDialog";
import type { Section as SettingsSection } from "./components/settings/SettingsDialog";
import { COPY } from "./lib/copy";
import { ProjectDialog } from "./components/projects/ProjectDialog";
import { confirmDialog } from "./lib/confirm";
const TasksPage = lazy(() => import("./components/tasks/TasksPage").then(m => ({ default: m.TasksPage })));
const SettingsDialog = lazy(() => import("./components/settings/SettingsDialog").then(m => ({ default: m.SettingsDialog })));

// Tools whose output the user watches in the 文件 face.
const FILE_TOOLS = new Set([
  "file_write", "file_read", "doc_export", "doc_read", "chart", "qrcode",
  "csv_to_json", "csv_to_xlsx", "xlsx_to_csv", "csv_join", "sql_query", "pdf_extract", "slides",
]);

// liveTab derives where the agent is working RIGHT NOW from the latest event, so
// the drawer can follow it (Live Focus). Null when idle. The live browser now
// renders inline in the thread, so browser activity no longer steers the drawer.
function liveTabFromMessages(messages: Message[], streaming: boolean): Tab | null {
  if (!streaming) return null;
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m.type === "tool") {
      const tool = (m.payload as { tool?: string })?.tool || "";
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
  { version: "auto", label: "自动选择", hint: "使用列表中的第一个模型" },
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
    setOnUnauthorized(() => { clearFollowUps(); setUser(null); });
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
        <Workbench key={user.email} user={user} onSignOut={() => { auth.clear(); clearFollowUps(); setUser(null); }} />
      )}
      <Toaster />
      <ConfirmHost />
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
  const [ownedListLoaded, setOwnedListLoaded] = useState(false);
  const [sharedListLoaded, setSharedListLoaded] = useState(false);
  const [shareFor, setShareFor] = useState<Conversation | null>(null); // open share dialog
  const [drawerArtifact, setDrawerArtifact] = useState<string | null>(null); // artifact to open inline in the drawer
  const openArtifactInDrawer = useCallback((id: string) => { setDrawerArtifact(id); setDrawerOpen(true); setDrawerTab("artifacts"); }, []);
  const activeStorageKey = `orka.activeConversation.${user.email}`;
  // ?c=<id> opens that conversation (the link in outbound task notifications).
  const [activeID, setActiveID] = useState(() => new URLSearchParams(location.search).get("c") || localStorage.getItem(activeStorageKey) || "");
  useEffect(() => {
    const params = new URLSearchParams(location.search);
    if (!params.has("c")) return;
    params.delete("c");
    const query = params.toString();
    history.replaceState(null, "", location.pathname + (query ? "?" + query : "") + location.hash);
  }, []);
  const restoreSelection = useRef<string | null>(activeID);
  useEffect(() => {
    if (activeID) localStorage.setItem(activeStorageKey, activeID);
    else localStorage.removeItem(activeStorageKey);
  }, [activeID, activeStorageKey]);
  const [sessionRecovery] = useState(() => new SessionRecoveryStore(user.email));
  const persistenceWarning = useSyncExternalStore(sessionRecovery.subscribe, sessionRecovery.getWarning);
  const conversationSettings = useConversationSettings(sessionRecovery, activeID);
  const conversationDraft = useConversationDraft(activeID, sessionRecovery);

  // Sidebar starts open on desktop, closed on narrow screens (where it overlays).
  const [sidebarOpen, setSidebarOpen] = useState(() =>
    typeof window === "undefined" ? true : window.innerWidth >= 768,
  );
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerTab, setDrawerTab] = useState<Tab>("files");
  const [modelSettingsOpen, setModelSettingsOpen] = useState(false);
  const version = conversationSettings.value.selectedVersion;
  const setVersion = (selectedVersion: string) => conversationSettings.patch({ selectedVersion });
  const [theme, toggleTheme] = useTheme();
  const [models, setModels] = useState<ModelOption[]>(MODELS_FALLBACK);
  const [modelsLoaded, setModelsLoaded] = useState(false);
  // First-run guide progress from the server; null until known, so the guide
  // never flashes for someone who already finished or skipped it.
  const [onboarding, setOnboarding] = useState<string[] | null>(null);
  useEffect(() => {
    api.me().then(u => setOnboarding(u.onboarding || [])).catch(() => setOnboarding(["dismissed"]));
  }, []);
  const markOnboarding = useCallback((step: "tour" | "dismissed") => {
    setOnboarding(o => (o?.includes(step) ? o : [...(o || []), step]));
    api.markOnboarding(step).catch(() => {});
  }, []);
  // The main area shows a conversation or the tasks page; a prompt sent to
  // "设为定时任务" opens the task editor prefilled with it.
  // The tasks page is addressable (#tasks) so it survives reloads and links.
  const [view, setViewState] = useState<View>(() => viewFromHash(location.hash));
  const setView = useCallback((next: View) => {
    setViewState(next);
    const hash = hashOf(next);
    if (location.hash !== hash) history.pushState(null, "", location.pathname + location.search + hash);
  }, []);
  useEffect(() => {
    const sync = () => setViewState(viewFromHash(location.hash));
    window.addEventListener("popstate", sync);
    return () => window.removeEventListener("popstate", sync);
  }, []);
  // Visiting the tasks page is the guide's last step, however the user got there.
  useEffect(() => {
    if (view.kind === "tasks" && onboarding && !onboarding.includes("tour")) markOnboarding("tour");
  }, [view, onboarding, markOnboarding]);
  const [settingsOpen, setSettingsOpen] = useState<false | SettingsSection>(false);
  const [taskDraft, setTaskDraft] = useState<TaskDraft | null>(null);
  const openSchedule = useCallback((prompt: string) => {
    setTaskDraft({ title: "", prompt, trigger: { kind: "schedule", cron: "0 9 * * 1-5" }, enabled: true, retry_count: 0 });
    setView(TASKS);
  }, []);
  const activeSkill = conversationSettings.value.activeSkill;
  const setActiveSkill = (activeSkill: string | null) => conversationSettings.patch({ activeSkill });
  // Gate side-effecting tools (terminal/browser/network/code) behind approval.
  const confirmRisky = conversationSettings.value.confirmRisky;
  const toggleConfirm = () => conversationSettings.patch({ confirmRisky: !confirmRisky });
  // Review the plan before anything runs: off by default, like deep thinking a
  // per-browser preference.
  const [planReview, setPlanReview] = useState(() => { try { return localStorage.getItem("orka.planReview") === "1"; } catch { return false; } });
  const togglePlanReview = useCallback(() => setPlanReview((v) => { try { localStorage.setItem("orka.planReview", v ? "0" : "1"); } catch { /* preference only */ } return !v; }), []);
  // Deep thinking, per task. Sent as INTENT ("on"/"off"), never as a provider
  // field: which field a model understands differs per model (glm takes
  // `thinking`, OpenAI `reasoning_effort`, Qwen `enable_thinking`), and the
  // model is picked from the dropdown right next to this switch — a switch that
  // sent the field would silently stop working on the next model.
  //
  // Off by default, which is what the deployment configures. Measured on one
  // question: reducing it cut reasoning tokens 62% and latency 37%. It is worth
  // turning ON for genuinely hard reasoning, which is why it is a switch and
  // not a constant.
  const [deepThinking, setDeepThinking] = useState(() => localStorage.getItem("orka.deepThinking") === "1");
  const toggleThinking = useCallback(() => setDeepThinking((v) => { localStorage.setItem("orka.deepThinking", v ? "0" : "1"); return !v; }), []);

  useEffect(() => {
    api.models().then((m) => { if (m.length) setModels(m); }).catch(() => {}).finally(() => setModelsLoaded(true));
  }, []);
  // conversation_ids that belong to a task → listed under 任务对话 in the sidebar.
  const [scheduledIds, setScheduledIds] = useState<Set<string>>(new Set());

  const { run, steer, kill, hydrateMessages, messagesOf, statusOf, runningIds, connectionOf, errorOf, inputOf } = useChatStreams(sessionRecovery);
  const [runRevision, setRunRevision] = useState(0);
  const messages = messagesOf(activeID);
  const status = statusOf(activeID);
  // Where the agent is working right now → the drawer follows it (Live Focus).
  const liveTab = liveTabFromMessages(messages, status === "streaming");
  // conversations whose history we've already loaded (or that have a live run),
  // so switching back never clobbers an in-flight stream with stale history.
  const seen = useRef<Set<string>>(new Set());
  // Empty history is retryable, but distinguish it from history still loading.
  const [historyLoaded, setHistoryLoaded] = useState<Set<string>>(new Set());

  const [cmdOpen, setCmdOpen] = useState(false);
  const [keysOpen, setKeysOpen] = useState(false); // "?" shortcut sheet

  // The floating composer's height becomes the thread's bottom padding, so every
  // message can be scrolled fully above it no matter how tall the input grows.
  const composerRef = useRef<HTMLDivElement>(null);
  const [composerH, setComposerH] = useState(0);
  useEffect(() => {
    const el = composerRef.current;
    if (!el) return;
    const measure = () => setComposerH(el.offsetHeight);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [activeID]);

  // Global keyboard shortcuts (mirrors what ChatGPT/Claude/Cursor offer):
  //   ⌘/Ctrl+K  → command palette
  //   ⌘/Ctrl+J  → new chat
  //   ⌘/Ctrl+B  → toggle the sidebar
  //   ?         → shortcut sheet (only outside a text field)
  //   Esc       → stop the running task (when one is streaming)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = document.activeElement as HTMLElement | null;
      const typing = !!el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable);
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setCmdOpen((o) => !o);
      } else if (mod && e.key.toLowerCase() === "j") {
        e.preventDefault();
        setActiveID(""); // same as newConversation(), which is declared below
      } else if (mod && e.key.toLowerCase() === "b") {
        e.preventDefault();
        setSidebarOpen((o) => !o);
      } else if (e.key === "?" && !typing && !mod) {
        // "?" is a character, so it must never fire while composing a message.
        e.preventDefault();
        setKeysOpen((o) => !o);
      } else if (e.key === "Escape" && statusOf(activeID) === "streaming" && !typing) {
        kill(activeID);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [activeID, statusOf, kill]);

  // Pull tasks to learn which conversations belong to a task, enabled or not:
  // the sidebar lists those under 任务对话.
  const refreshTasks = useCallback(() => {
    api
      .getTasks()
      .then((r) => {
        const ids = new Set<string>();
        for (const t of r.tasks || []) {
          if (t.conversation_id) ids.add(t.conversation_id);
        }
        setScheduledIds(ids);
      })
      .catch(() => {});
  }, []);

  const [projects, setProjects] = useState<Project[]>([]);
  const [projectsLoaded, setProjectsLoaded] = useState(false);
  const refreshProjects = useCallback(() => {
    return api.listProjects().then(p => { setProjects(p || []); setProjectsLoaded(true); }).catch(() => setProjectsLoaded(true));
  }, []);
  // Projects change in other tabs and devices too: reload on mount and
  // whenever this tab comes back into view.
  useEffect(() => {
    void refreshProjects();
    const onVisible = () => { if (document.visibilityState === "visible") void refreshProjects(); };
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("focus", onVisible);
    return () => { document.removeEventListener("visibilitychange", onVisible); window.removeEventListener("focus", onVisible); };
  }, [refreshProjects]);
  const [projectDialog, setProjectDialog] = useState<null | { project?: Project }>(null);
  // A link to a project this tab has not seen yet (made elsewhere): look again once.
  const checkedProject = useRef("");
  useEffect(() => {
    if (view.kind !== "project" || !projectsLoaded || projects.some(p => p.project_id === view.id) || checkedProject.current === view.id) return;
    checkedProject.current = view.id;
    setProjectsLoaded(false);
    void refreshProjects();
  }, [view, projects, projectsLoaded, refreshProjects]);
  // A new conversation started from a project page is created inside it, lazily
  // like any other, on its first message.
  const [draftProject, setDraftProject] = useState("");
  const draftProjectRef = useRef("");
  draftProjectRef.current = draftProject;
  const [moveFor, setMoveFor] = useState<Conversation | null>(null);
  const refreshConversations = useCallback(() => {
    api.listConversations().then((c) => { setConversations(c || []); setOwnedListLoaded(true); }).catch(() => {});
    api.sharedWithMe().then((c) => { setShared(c || []); setSharedListLoaded(true); }).catch(() => {});
  }, []);

  // load conversation list + tasks on mount (persists across refresh)
  useEffect(() => {
    refreshConversations();
    refreshTasks();
  }, [refreshConversations, refreshTasks]);

  // Live event bus: push-refresh the affected resources the instant background
  // work signals a change, instead of waiting for the next poll tick. Polling
  // stays on as the fallback when the stream can't connect.
  useEventStream(
    useCallback((kind: string) => {
      if (kind === "notification") refreshResource("notifications");
      if (["file", "artifact", "run"].includes(kind)) invalidateSessionFiles();
      if (kind === "run") {
        setRunRevision(n => n + 1);
        refreshResource("runs:all");
        refreshResource("runs:failed");
        refreshResource("metrics");
        refreshResource("notifications");
        refreshResourcePrefix("runs:task:");
        refreshTasks();
      }
    }, [refreshTasks]),
  );

  // The active conversation may be one I own or one shared with me; resolve it
  // and my role so the composer can go read-only for viewers.
  const activeConv = conversations.find((c) => c.conversation_id === activeID) || shared.find((c) => c.conversation_id === activeID);
  const isShared = !!activeConv?.owner_email && activeConv.owner_email !== user.email;
  const readOnly = isShared && !(activeConv?.shares ?? []).some((s) => s.email === user.email && s.role === "editor");

  // "新对话" just resets to the empty state — the server conversation is
  // created lazily on the first send (ensureConversation), so clicking around
  // never accumulates empty "新对话" placeholders.
  const newConversation = useCallback(() => { setActiveID(""); setDraftProject(""); setView(CHAT); }, []);
  const newConversationInProject = useCallback((projectID: string) => { setActiveID(""); setDraftProject(projectID); setView(CHAT); }, []);
  // Project lifecycle: create and rename through a dialog, delete after
  // confirmation. Deleting returns its conversations to the general list.
  const saveProject = useCallback(async (v: { name: string; description: string }, existing?: Project) => {
    const p = await api.saveProject(existing ? { ...existing, ...v } : v);
    setProjects(ps => existing ? ps.map(x => x.project_id === p.project_id ? p : x) : [p, ...ps]);
    if (!existing) setView({ kind: "project", id: p.project_id });
  }, [setView]);
  const deleteProject = useCallback(async (project: Project) => {
    if (!await confirmDialog({ title: `删除项目“${project.name}”？`, body: "项目里的对话会回到对话列表，对话里的文件不会被删除。", confirmText: "删除项目", danger: true })) return;
    try {
      await api.deleteProject(project.project_id);
      setProjects(ps => ps.filter(x => x.project_id !== project.project_id));
      setConversations(cs => cs.map(c => c.project_id === project.project_id ? { ...c, project_id: undefined } : c));
      setView(CHAT);
      toast("项目已删除", "success");
    } catch (e) { toast((e as Error).message || "删除失败", "error"); }
  }, [setView]);

  const onPrune = useCallback(async () => {
    await api.pruneConversations().catch(() => {});
    refreshConversations();
  }, [refreshConversations]);

  // rejoinIfLive attaches to a run the server says is running for this
  // conversation but this tab is not reading — a page refresh mid-run, a second
  // tab or device, or a run a schedule, webhook or the API started. Without it
  // the conversation opened as a frozen snapshot while the run carried on
  // unseen: no live thinking, no new steps, no 运行中, so steering and stop were
  // unavailable too. The replay this triggers is safe: frames whose id is
  // already in the loaded history are skipped.
  const rejoinIfLive = useCallback(
    async (id: string) => {
      if (!id || statusOf(id) === "streaming") return;
      const { active } = await api.active(id);
      if (active) run({ message: "", conversationID: id, userEmail: user.email, attachOnly: true, rejoin: true });
    },
    [run, statusOf, user.email],
  );

  const selectConversation = useCallback(
    async (id: string) => {
      setActiveID(id);
      setView(CHAT);
      if (seen.current.has(id)) {
        void rejoinIfLive(id); // loaded before, but a run may have started since
        return;
      }
      seen.current.add(id);
      setHistoryLoaded(ids => { const next = new Set(ids); next.delete(id); return next; });
      try {
        const rows = (await api.getMessages(id)) as (Message & { created_at?: number })[];
        hydrateMessages(id, rows.map((r) => ({ ...r, ts: r.ts || r.created_at || Date.now() })).sort((a, b) => a.ts - b.ts));
        if (rows.length === 0) seen.current.delete(id);
        setHistoryLoaded(ids => new Set(ids).add(id));
      } catch {
        seen.current.delete(id);
      }
      // AFTER the history is in: loading it replaces the message list, which
      // would wipe anything a rejoin had already streamed in.
      void rejoinIfLive(id);
    },
    [hydrateMessages, rejoinIfLive],
  );

  // Restore once, after BOTH lists arrive. Refreshes must never clear a newly
  // created conversation, and a late shared list must not erase its selection.
  useEffect(() => {
    const initialID = restoreSelection.current;
    if (!initialID) return;
    if (activeID !== initialID) { restoreSelection.current = null; return; }
    if (!ownedListLoaded || !sharedListLoaded) return;
    restoreSelection.current = null;
    const exists = conversations.some(c => c.conversation_id === activeID) || shared.some(c => c.conversation_id === activeID);
    if (exists) void selectConversation(activeID);
    else setActiveID("");
  }, [activeID, conversations, shared, selectConversation, ownedListLoaded, sharedListLoaded]);

  const creatingConversation = useRef({ activeID, generation: 0, promise: null as Promise<string> | null });
  if (creatingConversation.current.activeID !== activeID) {
    creatingConversation.current.activeID = activeID;
    creatingConversation.current.generation++;
    creatingConversation.current.promise = null;
  }
  const ensureConversation = useCallback((): Promise<string> => {
    const state = creatingConversation.current;
    if (state.activeID) return Promise.resolve(state.activeID);
    if (state.promise) return state.promise;
    const generation = state.generation;
    const promise = api.createConversation("新对话", draftProjectRef.current || undefined).then(c => {
      setConversations(cs => cs.some(item => item.conversation_id === c.conversation_id) ? cs : [c, ...cs]);
      // A late creation must not replace a conversation selected in the meantime.
      if (state.generation === generation && !state.activeID) {
        conversationSettings.move("", c.conversation_id);
        state.activeID = c.conversation_id;
        setActiveID(c.conversation_id);
      }
      return c.conversation_id;
    }).finally(() => { if (state.promise === promise) state.promise = null; });
    state.promise = promise;
    return promise;
  }, [conversationSettings.move]);

  // After approving a paused danger tool the backend resumes the checkpointed
  // run, which streams on a NEW SSE this client isn't reading — re-attach so the
  // continuation (and the final answer) actually lands in the thread.
  const onResumed = useCallback(
    (cid: string) => {
      return run({ message: "", conversationID: cid, userEmail: user.email, attachOnly: true });
    },
    [run, user.email],
  );

  const runActions = useRunActions(onResumed);
  const recovery = useRunRecovery({ conversationID: activeID, messages, status, enabled: !isShared, historyLoaded: historyLoaded.has(activeID) }, onResumed, runRevision, runActions.actions);


  const lastMsgRef = useRef("");
  const onSend = useCallback(
    async (msg: string, fileIDs: string[] = [], conversationID?: string) => {
      // Capture the request before any asynchronous conversation creation. The
      // composer may already have created its target while the user navigated.
      const request = { message: msg, userEmail: user.email, selectedVersion: version, activeSkill: activeSkill ?? "", fileIDs: [...fileIDs], confirmRisky, deepThinking: deepThinking ? "on" : "off", planReview };
      const id = conversationID || activeID || await ensureConversation();
      if (statusOf(id) === "streaming" || recovery.isBusy(id) || runActions.actions.isBusy(id)) {
        await steer(id, msg, fileIDs);
        return;
      }
      lastMsgRef.current = msg;
      seen.current.add(id);
      // Await only server acceptance. The task keeps streaming independently.
      await new Promise<void>((resolve, reject) => {
        void run({ ...request, conversationID: id, onAccepted: resolve, onRejected: reject }).then(() => refreshConversations());
      });
      refreshTasks();
    },
    [ensureConversation, run, steer, user.email, refreshTasks, refreshConversations, version, activeSkill, confirmRisky, deepThinking, planReview, recovery.isBusy, runActions.actions, activeID, statusOf],
  );

  // Re-send the last user message after a failure (network drop, sandbox down…).
  const onRetry = useCallback(() => {
    const input = inputOf(activeID);
    if (input && !runActions.actions.isBusy(activeID)) void run({ ...input });
  }, [activeID, inputOf, run, runActions.actions]);

  const onRename = useCallback(async (id: string, title: string) => {
    try {
      await api.renameConversation(id, title);
    } catch {
      toast("重命名失败,请重试", "error");
      return; // don't apply a rename the server rejected
    }
    setConversations((cs) => cs.map((c) => (c.conversation_id === id ? { ...c, title } : c)));
  }, []);

  const onDelete = useCallback(
    async (id: string) => {
      try {
        await api.deleteConversation(id);
      } catch {
        toast("删除失败,请重试", "error");
        return; // keep it in the list — it still exists on the server
      }
      setConversations((cs) => cs.filter((c) => c.conversation_id !== id));
      seen.current.delete(id);
      if (activeID === id) setActiveID("");
    },
    [activeID],
  );

  const onResume = useCallback(
    async (resumeKey: string, answer: string, approvedPlan?: string[]) => {
      run({ message: answer, conversationID: activeID, userEmail: user.email, resumeKey, selectedVersion: version, ...(approvedPlan ? { approvedPlan } : {}) });
      refreshTasks();
    },
    [run, activeID, user.email, refreshTasks, version],
  );

  // Branch the active conversation at a turn: the backend copies history up to
  // that message into a new conversation; we add it to the list and switch to it.
  const onFork = useCallback(
    async (messageID: string) => {
      if (!activeID) return;
      try {
        const branch = await api.forkConversation(activeID, messageID);
        setConversations((cs) => [branch, ...cs]);
        seen.current.delete(branch.conversation_id); // force a fresh message load
        await selectConversation(branch.conversation_id);
        toast("已创建分支", "success");
      } catch {
        toast("创建分支失败,请重试", "error");
      }
    },
    [activeID, selectConversation],
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

  // ⌘K command palette: collapse the app's scattered actions into one entry.
  const openPanel = (t: Tab) => { setDrawerTab(t); setDrawerOpen(true); };
  const commands: Command[] = [
    { id: "new", group: "操作", icon: "plus", label: "新对话", hint: "⌘J", run: newConversation },
    { id: "share", group: "操作", icon: "share", label: "分享当前对话", run: () => activeConv && setShareFor(conversations.find((c) => c.conversation_id === activeID) || null) },
    { id: "schedule", group: "操作", icon: "clock", label: "把上一条设为定时任务", run: () => lastMsgRef.current && openSchedule(lastMsgRef.current) },
    { id: "tasks", group: "操作", icon: "clock", label: "打开定时任务", keywords: "schedule trigger task 触发", run: () => setView(TASKS) },
    { id: "settings", group: "设置", icon: "gear", label: "设置", keywords: "settings connector mcp 集成 个性化 记忆 memory", run: () => setSettingsOpen("personal") },
    { id: "theme", group: "设置", icon: theme === "dark" ? "sun" : "moon", label: theme === "dark" ? "切换到亮色" : "切换到暗色", run: toggleTheme },
    { id: "confirm", group: "设置", icon: "shield", label: confirmRisky ? "关闭高危操作确认" : "开启高危操作确认", run: toggleConfirm },
    { id: "keys", group: "帮助", icon: "keyboard", label: "键盘快捷键", hint: "?", keywords: "shortcut keyboard 快捷键", run: () => setKeysOpen(true) },
    ...models.map((m): Command => ({ id: "model:" + m.version, group: "切换模型", icon: "sparkle", label: m.label, hint: m.hint, keywords: m.version, run: () => setVersion(m.version) })),
    ...([
      ["files", "文件"], ["artifacts", "页面"], ["runs", "运行记录"],
    ] as [Tab, string][]).map(([t, label]): Command => ({ id: "panel:" + t, group: "打开面板", icon: "panel", label, run: () => openPanel(t) })),
    ...conversations.slice(0, 60).map((c): Command => ({ id: "conv:" + c.conversation_id, group: "跳转对话", icon: "chat", label: c.title || "未命名对话", keywords: c.title, run: () => selectConversation(c.conversation_id) })),
  ];

  return (
    <div className="relative flex h-screen">
      {cmdOpen && <CommandPalette commands={commands} onClose={() => setCmdOpen(false)} />}
      {keysOpen && <ShortcutSheet onClose={() => setKeysOpen(false)} />}
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
        projects={projects}
        activeProjectID={view.kind === "project" ? view.id : ""}
        onOpenProject={(id) => setView({ kind: "project", id })}
        onNewProject={() => setProjectDialog({})}
        onRenameProject={(id) => setProjectDialog({ project: projects.find(p => p.project_id === id) })}
        onDeleteProject={(id) => { const p = projects.find(x => x.project_id === id); if (p) void deleteProject(p); }}
        onMove={(id) => setMoveFor(conversations.find((c) => c.conversation_id === id) || null)}
        name={user.name}
        email={user.email}
        onSignOut={onSignOut}
        tasksActive={view.kind === "tasks"}
        onOpenTasks={() => setView(TASKS)}
        onOpenSettings={() => setSettingsOpen("personal")}
      />

      {view.kind === "project" ? (() => {
        const project = projects.find(p => p.project_id === view.id);
        if (!project) return <main className="flex min-w-0 flex-1 flex-col items-center justify-center gap-3 text-sm text-muted">
          <p>{projectsLoaded ? "这个项目不存在或已被删除。" : "正在加载项目…"}</p>
          <button onClick={() => setView(CHAT)} className="text-accent hover:underline">返回对话</button>
        </main>;
        return <ProjectPage project={project} email={user.email}
          conversations={conversations.filter(c => c.project_id === project.project_id)}
          onSaved={p => setProjects(ps => ps.map(x => x.project_id === p.project_id ? p : x))}
          onRename={() => setProjectDialog({ project })}
          onDelete={() => void deleteProject(project)}
          onNewConversation={() => newConversationInProject(project.project_id)}
          onOpenConversation={id => { void selectConversation(id); }}
          onToggleSidebar={() => setSidebarOpen(o => !o)} />;
      })() : view.kind === "tasks" ? <main className="relative flex min-w-0 flex-1 flex-col">
        <Suspense fallback={<p role="status" className="p-6 text-sm text-muted">正在加载定时任务…</p>}>
          <TasksPage onOpenConversation={id => { void selectConversation(id); }} onToggleSidebar={() => setSidebarOpen(o => !o)} initialDraft={taskDraft} onDraftOpened={() => setTaskDraft(null)} />
        </Suspense>
      </main> : <main className="relative flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-1 px-2 sm:gap-3 sm:px-4">
          <button
            onClick={() => setSidebarOpen((o) => !o)}
            className="grid h-8 w-8 place-items-center rounded-lg text-muted hover:bg-surface2"
            title={COPY.sidebarHint}
            aria-label={COPY.toggleSidebar}
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
              <path d="M3 6h18M3 12h18M3 18h18" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            </svg>
          </button>
          {/* Show the active conversation (with live status) instead of a
              redundant brand — "Orka" already lives in the sidebar. */}
          <div className="flex min-w-0 items-center gap-1.5">
            {status === "streaming" && <span className="inline-flex shrink-0 items-center" role="status"><span className="h-1.5 w-1.5 animate-pulse rounded-full bg-ok" title="运行中" /><span className="sr-only">运行中</span></span>}
            <span className="max-w-[40vw] truncate text-md text-ink md:max-w-[260px]" title={activeConv?.title || COPY.newChat}>
              {activeConv?.title || COPY.newChat}
            </span>
            {isShared && <span className="shrink-0 text-2xs text-faint" title={`由 ${activeConv?.owner_email} 分享`}>· 共享</span>}
            {(() => {
              const pid = activeID ? activeConv?.project_id : draftProject;
              const project = pid ? projects.find(p => p.project_id === pid) : undefined;
              return project && <button onClick={() => setView({ kind: "project", id: project.project_id })} title="打开项目"
                className="inline-flex max-w-[30vw] shrink-0 items-center gap-1 rounded-full border border-border px-2 py-0.5 text-xs text-muted hover:text-ink">
                <Icon name="folder" size={12} /><span className="truncate">{project.name}</span>
              </button>;
            })()}
          </div>
          <ModelSelect value={version} onChange={setVersion} models={models} onManage={() => setModelSettingsOpen(true)} />
          <span className="flex-1" />
          <NotificationBell onJump={onJumpToConversation} />
          <ActionChip
            variant="headerChip" size="headerChip" icon="table"
            onClick={() => setDrawerOpen((o) => !o)}
            aria-label="切换工作台面板"
            aria-pressed={drawerOpen}
            title="工作台：文件 · 页面 · 运行记录"
            className="shrink-0"
          ><span className="hidden sm:inline">工作台</span></ActionChip>
          <HeaderMenu
            onShare={activeConv && !isShared ? () => setShareFor(activeConv) : undefined}
            onModelSettings={() => setModelSettingsOpen(true)}
            theme={theme} onToggleTheme={toggleTheme}
            onShortcuts={() => setKeysOpen(true)}
          />
        </header>

        {persistenceWarning && <p role="alert" className="mx-5 mb-2 text-xs text-accent">{persistenceWarning}</p>}
        {(connectionOf(activeID) === "disconnected" || connectionOf(activeID) === "stopping" || errorOf(activeID)) && (
          <div className="mx-5 mb-2 flex flex-wrap items-center gap-2 text-xs" role="status">
            {connectionOf(activeID) === "disconnected" && <span className="text-muted">任务连接已中断。</span>}
            {connectionOf(activeID) === "stopping" && <span className="text-muted">正在停止，等待服务确认…</span>}
            {errorOf(activeID) && <span role="alert" className="text-accent">{errorOf(activeID)}</span>}
            {connectionOf(activeID) === "disconnected" && !readOnly && <>
              <ActionChip onClick={() => void onResumed(activeID)} icon="refresh">重新连接</ActionChip>
              <ActionChip onClick={() => void kill(activeID)}>再次请求停止</ActionChip>
            </>}
          </div>
        )}
        {!readOnly && messages.length === 0 && onboarding && modelsLoaded && (() => {
          const state = { hasModel: models.length > 1, hasConversation: conversations.length > 0, toured: onboarding.includes("tour") };
          return onboardingVisible(state, onboarding.includes("dismissed")) && <Onboarding state={state}
            onConfigureModel={() => setModelSettingsOpen(true)}
            onTry={() => { conversationDraft.setText(TRY_PROMPT); document.getElementById("chat-input")?.focus(); }}
            onTour={() => setView(TASKS)}
            onDismiss={() => markOnboarding("dismissed")}
            onEnableMemory={async () => { const p = await api.getPersonal(); await api.savePersonal({ ...p, auto_memory: true }); }} />;
        })()}
        <Thread onExample={text => { conversationDraft.setText(text); document.getElementById("chat-input")?.focus(); }} conversationID={activeID} ownerEmail={activeConv?.owner_email || user.email} recovery={recovery} onContinue={() => void recovery.resume()} onCancelAutoResume={() => void recovery.cancelAutoResume()} canRetry={!readOnly && !!inputOf(activeID) && recovery.run?.status !== "running"} messages={messages} status={status} onResume={onResume} onResumed={onResumed} onPick={msg => { void onSend(msg).catch(e => toast(e.message || "发送失败", "error")); }} onRetry={onRetry} onSchedule={openSchedule} onFork={onFork} fileConv={isShared ? activeID : undefined} bottomInset={composerH} />
        {/* The composer floats OVER the thread (its height is fed back as the
            thread's bottom padding), so the conversation scrolls clear of it
            instead of the last lines being clipped behind the tool row. */}
        <div ref={composerRef} className="absolute bottom-0 left-0 right-[var(--sbw)] z-20">
          <div aria-hidden className="pointer-events-none h-8 bg-gradient-to-b from-transparent to-bg" />
          <div className="bg-bg">
            {activeID && <div className="px-5"><ArtifactBanner conversationId={activeID} onOpen={openArtifactInDrawer} /></div>}
            {readOnly ? (
              <div className="mx-auto mb-4 w-full max-w-3xl px-5">
                <div className="flex items-center justify-center gap-1.5 rounded-xl border border-border bg-surface2/50 px-4 py-3 text-center text-sm text-muted">
                  <Icon name="eye" size={14} />只读对话 · 由 {activeConv?.owner_email} 分享 · 你无法发送消息
                </div>
              </div>
            ) : (
              <Composer draftState={conversationDraft} sessionRecovery={sessionRecovery} conversationID={activeID} ensureConversation={ensureConversation} blocked={recovery.busy || runActions.actions.isBusy(activeID) || connectionOf(activeID) === "stopping"} status={status} onSend={onSend} onKill={() => kill(activeID)} activeSkill={activeSkill} onPickSkill={setActiveSkill} runSettings={{ deepThinking, confirmRisky, planReview, toggleThinking, toggleConfirm, togglePlanReview }} onManageSkills={() => setSettingsOpen("skills")} />
            )}
          </div>
        </div>
      </main>}

      {settingsOpen && <Suspense fallback={null}><SettingsDialog initial={settingsOpen} onClose={() => setSettingsOpen(false)} /></Suspense>}
      {drawerOpen && <Suspense fallback={<div role="status" className="p-4">正在加载工作台…</div>}><ArtifactDrawer
        onResumeRun={runActions.resume}
        canResumeRun={record => runActions.actions.canResume(record)}
        isRunBusy={cid => runActions.actions.isBusy(cid)}
        runRevision={runRevision}
        runContext={{ conversationID: activeID, messages, run: recovery.run }}
        conversationID={activeID}
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        tab={drawerTab}
        setTab={setDrawerTab}
        liveTab={liveTab}
        email={user.email}
        onJumpToConversation={onJumpToConversation}
        focusArtifact={drawerArtifact}
        onClearArtifact={() => setDrawerArtifact(null)}
      /></Suspense>}


      {modelSettingsOpen && <ModelSettings onClose={() => setModelSettingsOpen(false)} onSaved={() => { setVersion("auto"); api.models().then(m => setModels(m.length ? m : MODELS_FALLBACK)).catch(() => {}); }} />}
      {projectDialog && <ProjectDialog project={projectDialog.project} onClose={() => setProjectDialog(null)}
        onSubmit={v => saveProject(v, projectDialog.project)} />}
      {moveFor && <MoveToProjectDialog conv={moveFor} projects={projects} onClose={() => setMoveFor(null)}
        onMoved={(id, projectID) => setConversations(cs => cs.map(c => c.conversation_id === id ? { ...c, project_id: projectID || undefined } : c))} />}
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

// ModelSelect is the header model picker; it sets the per-run `selected_version`
// the backend resolves ("auto" → list default; otherwise an explicit model ID).
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
        className="relative grid h-8 w-8 place-items-center rounded-lg text-muted hover:bg-surface2"
      >
        <Icon name="bell" />
        {unread > 0 && (
          <span className="absolute -right-0.5 -top-0.5 grid h-4 min-w-[16px] place-items-center rounded-full bg-accent px-1 text-2xs text-white">{unread > 9 ? "9+" : unread}</span>
        )}
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-10" onClick={() => setOpen(false)} />
          <div className="absolute right-0 z-20 mt-1 w-80 rounded-xl border border-border bg-surface p-1.5 shadow-lg">
            <div className="flex items-center justify-between px-2 py-1">
              <span className="text-2xs font-medium uppercase tracking-wide text-faint">通知</span>
              {items.length > 0 && <button onClick={() => api.readNotifications().then(() => { load(); refreshResource("notifications"); })} className="text-2xs text-faint hover:text-accent">全部已读</button>}
            </div>
            {items.length === 0 && (
              <div className="px-3 py-6 text-center">
                <div className="text-sm text-ink">没有新通知</div>
                <p className="mt-1 text-xs text-muted">后台任务跑完、定时任务触发或有人分享对话时,会在这里提醒你。</p>
              </div>
            )}
            <div className="max-h-[60vh] overflow-y-auto">
              {items.map((n) => {
                const markRead = () => { if (!n.read) api.readNotifications(n.notification_id).then(() => { load(); refreshResource("notifications"); }); };
                const jump = () => { markRead(); if (n.conversation_id) { onJump(n.conversation_id); setOpen(false); } };
                return (
                  <div key={n.notification_id} className={"rounded-lg px-2 py-1.5 hover:bg-surface2 " + (n.read ? "opacity-60" : "")}>
                    <button onClick={jump} className="flex w-full flex-col items-start gap-0.5 text-left">
                      <span className="text-sm text-ink">
                        {!n.read && <span className="mr-1 inline-block h-1.5 w-1.5 rounded-full bg-accent align-middle" />}
                        {n.title}
                      </span>
                      <span className="line-clamp-2 text-xs text-muted">{n.body}</span>
                    </button>
                    {/* Actionable: a failed unattended run can be re-fired or opened
                        without digging through the 运行 panel. */}
                    <div className="mt-1 flex items-center gap-3 text-2xs">
                      {n.run_id && (
                        <button
                          onClick={(e) => { e.stopPropagation(); markRead(); api.rerunRun(n.run_id).then(() => toast("已重新触发", "success")).catch(() => toast("重跑失败,请重试", "error")); }}
                          className="inline-flex items-center gap-1 text-faint hover:text-accent"
                        >
                          <Icon name="refresh" size={11} /> 重跑
                        </button>
                      )}
                      {n.conversation_id && (
                        <button onClick={(e) => { e.stopPropagation(); jump(); }} className="inline-flex items-center gap-1 text-faint hover:text-accent">
                          <Icon name="share" size={11} /> 查看对话
                        </button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        </>
      )}
    </div>
  );
}

// ModelSelect — a Radix DropdownMenu (shadcn/ui). Keyboard nav, focus return,
// typeahead and outside-click/Esc come from the primitive (replacing the old
// hand-rolled fixed-overlay menu), themed with the project's warm-paper tokens.
function ModelSelect({ value, onChange, models, onManage }: { value: string; onChange: (v: string) => void; models: ModelOption[]; onManage: () => void }) {
  const cur = models.find((m) => m.version === value) || models[0] || MODELS_FALLBACK[0];
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label="选择模型"
        className="flex min-w-0 items-center gap-1 rounded-full bg-surface2 px-2.5 py-1 text-xs text-muted outline-none hover:text-ink focus-visible:ring-2 focus-visible:ring-accent/50"
      >
        <span className="hidden sm:inline">模型：</span><span className="max-w-[120px] truncate">{cur.label}</span>
        <Icon name="chevron" size={12} className="shrink-0 text-faint" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-56">
        {models.map((m) => (
          <DropdownMenuItem
            key={m.version}
            onSelect={() => onChange(m.version)}
            className={"justify-between " + (m.version === value ? "bg-accentsoft text-accent data-[highlighted]:bg-accentsoft" : "")}
          >
            <span className="truncate">{m.label}</span>
            <span className="shrink-0 text-2xs text-faint">{m.hint}</span>
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onManage}><Icon name="gear" size={14} className="text-muted" />模型配置…</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// HeaderMenu holds what the header used to show as separate buttons but is
// needed rarely: sharing, model connections, theme, shortcuts.
function HeaderMenu({ onShare, onModelSettings, theme, onToggleTheme, onShortcuts }: {
  onShare?: () => void; onModelSettings: () => void; theme: string; onToggleTheme: () => void; onShortcuts: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger aria-label="更多" title="更多" className="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-muted outline-none hover:bg-surface2 focus-visible:ring-2 focus-visible:ring-accent/50">
        <Icon name="more" size={18} />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        {onShare && <DropdownMenuItem onSelect={onShare}><Icon name="share" size={14} className="text-muted" />分享对话</DropdownMenuItem>}
        <DropdownMenuItem onSelect={onModelSettings}><Icon name="gear" size={14} className="text-muted" />模型配置</DropdownMenuItem>
        <DropdownMenuItem onSelect={onToggleTheme}><Icon name={theme === "dark" ? "sun" : "moon"} size={14} className="text-muted" />{theme === "dark" ? "切换到亮色模式" : "切换到暗色模式"}</DropdownMenuItem>
        <DropdownMenuItem onSelect={onShortcuts}><Icon name="keyboard" size={14} className="text-muted" />键盘快捷键</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// ShortcutSheet is the "?" overlay. Power users look for it by reflex, and it is
// also the only place the non-obvious bindings (⌘J, ⌘B, Esc-to-stop) are stated.
const SHORTCUTS: { keys: string[]; label: string }[] = [
  { keys: ["⌘", "K"], label: "命令面板 · 跳对话 / 切模型 / 开面板" },
  { keys: ["⌘", "J"], label: "新对话" },
  { keys: ["⌘", "B"], label: "显示 / 隐藏侧栏" },
  { keys: ["↵"], label: "发送消息" },
  { keys: ["⇧", "↵"], label: "换行(不发送)" },
  { keys: ["@"], label: "在输入框引用工作区文件" },
  { keys: ["Esc"], label: "停止正在执行的任务" },
  { keys: ["?"], label: "打开 / 关闭这张表" },
];

function ShortcutSheet({ onClose }: { onClose: () => void }) {
  const panelRef = useRef<HTMLDivElement>(null);
  useOverlay(onClose, panelRef);
  return (
    <div className="overlay-in fixed inset-0 z-[60] flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-label="键盘快捷键"
        className="pop-in w-full max-w-md rounded-2xl border border-border bg-surface p-5 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-center gap-2">
          <Icon name="keyboard" size={16} className="text-muted" />
          <span className="flex-1 text-md font-medium text-ink">键盘快捷键</span>
          <button onClick={onClose} aria-label="关闭" className="text-faint hover:text-ink"><Icon name="close" size={15} /></button>
        </div>
        <div className="flex flex-col">
          {SHORTCUTS.map((s) => (
            <div key={s.label} className="flex items-center gap-3 border-b border-border/60 py-2 last:border-0">
              <span className="flex shrink-0 gap-1">
                {s.keys.map((k) => (
                  <kbd key={k} className="min-w-[24px] rounded-md border border-border bg-surface2 px-1.5 py-0.5 text-center font-mono text-xs text-ink">{k}</kbd>
                ))}
              </span>
              <span className="text-sm text-muted">{s.label}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
