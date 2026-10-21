import { clearSessionRecovery } from './lib/sessionRecovery';
import type { TaskTrigger } from './lib/taskTrigger';
import type { TaskToolPolicy } from './lib/taskToolPolicy';
import type {
  Connector,
  Artifact,
  ArtifactVersion,
  Conversation,
  MessageSearchHit,
  MetricsSnapshot,
  Message,
  Notification,
  OwnerInfo,
  OAuthProvider,
  Project,
  Personalization,
  Memory,
  RunRecord,
  TaskMeta,
} from "./types";
import { toastError } from "./lib/toast";

const BASE = "/api/v1/controller";
const TOKEN_KEY = "orka.token";

export const auth = {
  token: () => localStorage.getItem(TOKEN_KEY) || "",
  set: (t: string) => localStorage.setItem(TOKEN_KEY, t),
  clear: () => { clearSessionRecovery(); localStorage.removeItem(TOKEN_KEY); },
};

let unauthorizedHandler: (() => void) | null = null;
let unauthorizedProbe: { token: string; promise: Promise<boolean> } | null = null;
export function setOnUnauthorized(fn: () => void) {
  unauthorizedHandler = fn;
}

function expireSession(token: string) {
  if (!token || auth.token() !== token) return;
  auth.clear();
  unauthorizedHandler?.();
  toastError("登录已过期，请重新登录");
}

// A single endpoint can return 401 because its own authorization state is
// stale. Confirm the token against /auth/me before clearing the whole app.
async function confirmUnauthorized(path: string) {
  const token = auth.token();
  if (!token) return;
  if (path === "/auth/me") {
    expireSession(token);
    return;
  }
  if (!unauthorizedProbe || unauthorizedProbe.token !== token) {
    const probe = {
      token,
      promise: fetch(BASE + "/auth/me", {
        method: "POST",
        headers: { "Content-Type": "application/json", "Authorization": "Bearer " + token },
        body: "{}",
      }).then((res) => res.status === 401).catch(() => false),
    };
    unauthorizedProbe = probe;
    void probe.promise.finally(() => {
      if (unauthorizedProbe === probe) unauthorizedProbe = null;
    });
  }
  const probe = unauthorizedProbe;
  if (await probe.promise) expireSession(token);
}

function headers(json = true): Record<string, string> {
  const h: Record<string, string> = {};
  if (json) h["Content-Type"] = "application/json";
  const t = auth.token();
  if (t) h["Authorization"] = "Bearer " + t;
  return h;
}

// silent = a PROBE: absence is a normal answer, not something to interrupt the
// user with. ("does this conversation have an artifact?" runs on every message.)
async function post<T>(path: string, body: unknown, silent = false): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method: "POST",
      headers: headers(),
      body: JSON.stringify(body),
    });
  } catch {
    toastError("网络连接失败，请检查后端服务");
    throw new Error("network");
  }
  if (res.status === 401) {
    await confirmUnauthorized(path);
    throw new Error("unauthorized");
  }
  const j = await res.json().catch(() => ({}));
  if (j && typeof j.code === "number" && j.code !== 0) {
    if (!silent) toastError(j.msg || `请求失败 (${res.status})`);
    throw Object.assign(new Error(j.msg || "request failed"), { status: res.status });
  }
  if (!res.ok) {
    if (!silent) toastError("服务暂时不可用，请稍后再试");
    throw Object.assign(new Error("server " + res.status), { status: res.status });
  }
  return (j.data ?? j) as T;
}

async function get<T>(path: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, { headers: headers() });
  } catch {
    toastError("网络连接失败，请检查后端服务");
    throw new Error("network");
  }
  if (res.status === 401) {
    await confirmUnauthorized(path);
    throw new Error("unauthorized");
  }
  const j = await res.json().catch(() => ({}));
  if (!res.ok || (typeof j.code === "number" && j.code !== 0)) {
    throw new Error(j.msg || `请求失败 (${res.status})`);
  }
  return (j.data ?? j) as T;
}

export const chat = {
  steer: (conversation_id: string, run_id: string, request_id: string, message: string, file_ids: string[]) =>
    post<{ message: Message; run_id: string }>("/chat/steer", { conversation_id, run_id, request_id, message, file_ids }),
  // Approve/reject a paused side-effecting tool call.
  confirm: (id: string, approve: boolean, always = false, conversation_id = "") =>
    post<{ resolved: boolean; resumed?: boolean }>("/chat/confirm", { id, approve, always, conversation_id }),
};

export const artifacts = {
  list: () => post<{ artifacts: Artifact[] }>("/artifact/list", {}),
  byConversation: (conversation_id: string) => post<Artifact>("/artifact/by-conversation", { conversation_id }, true),
  get: (artifact_id: string, version = 0) =>
    post<{ artifact: Artifact; version: ArtifactVersion }>("/artifact/get", { artifact_id, version }),
  getBySlug: (slug: string, version = 0) =>
    post<{ artifact: Artifact; version: ArtifactVersion }>("/artifact/get", { slug, version }),
  versions: (artifact_id: string) => post<ArtifactVersion[]>("/artifact/versions", { artifact_id }),
  // share reuses the conversation_id field as the artifact id (server-side).
  share: (artifact_id: string, email: string, role: "viewer" | "editor" | "none") =>
    post<{ shares: { email: string; role: string }[]; visibility: string }>("/artifact/share", { conversation_id: artifact_id, email, role }),
  setPublic: (artifact_id: string, isPublic: boolean) =>
    post<{ visibility: string; share_token: string; slug: string }>("/artifact/visibility", { artifact_id, public: isPublic }),
  del: (artifact_id: string) => post("/artifact/delete", { artifact_id }),
  // The public page URL anyone-with-link can open.
  publicURL: (slug: string, token: string) => `${location.origin}/a/${slug}?t=${encodeURIComponent(token)}`,
};

// subscribeArtifact opens a fetch-based SSE stream of version numbers for an
// artifact (Bearer-authed, unlike EventSource). Calls onVersion on each frame;
// returns an unsubscribe function.
export function subscribeArtifact(artifactID: string, onVersion: (v: number) => void): () => void {
  const ctrl = new AbortController();
  (async () => {
    try {
      const res = await fetch(`${BASE}/artifact/stream?artifact_id=${encodeURIComponent(artifactID)}`, {
        headers: { ...(auth.token() ? { Authorization: "Bearer " + auth.token() } : {}) },
        signal: ctrl.signal,
      });
      if (!res.body) return;
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        const lines = buf.split("\n");
        buf = lines.pop() || "";
        for (const ln of lines) {
          if (ln.startsWith("data:")) {
            const v = parseInt(ln.slice(5).trim(), 10);
            if (!Number.isNaN(v)) onVersion(v);
          }
        }
      }
    } catch {
      /* aborted or network drop — caller re-subscribes if still mounted */
    }
  })();
  return () => ctrl.abort();
}

export interface Session {
  token: string;
  email: string;
  name: string;
}

export const accounts = {
  login: (email: string, password: string) =>
    rawAuth("/auth/login", { email, password }),
  register: (email: string, password: string, name: string) =>
    rawAuth("/auth/register", { email, password, name }),
};

async function rawAuth(path: string, body: unknown): Promise<Session> {
  const res = await fetch(BASE + path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const j = await res.json();
  if (j.code && j.code !== 0) throw new Error(j.msg || "auth failed");
  return j.data as Session;
}

export type ConnectorInput = Pick<Connector, "name" | "transport" | "url" | "headers">;
export interface SystemStatus { version: string; build_time: string; modified: boolean; started_at: string; ready: boolean; services: { name: string; status: string; detail: string }[]; model_probe: string }

export interface TaskInput {
  task_id?: string;
  title: string;
  prompt: string;
  trigger: TaskTrigger;
  enabled: boolean;
  retry_count: number;
  notify_webhook?: string;
  tool_policy: TaskToolPolicy;
}

// webhookURL is the public trigger address external systems call.
export const webhookURL = (token: string) => `${location.origin}${BASE}/hook/${token}`;

export const api = {
  me: () => post<{ email: string; name: string; onboarding?: string[] }>("/auth/me", {}),
  markOnboarding: (step: "tour" | "dismissed") => post<{ step: string }>("/auth/onboarding", { step }),
  listConversations: () => post<Conversation[]>("/conversation/list", {}),
  createConversation: (title: string, projectID?: string) =>
    post<Conversation>("/conversation/create-conversation", { title, ...(projectID ? { project_id: projectID } : {}) }),
  moveConversation: (conversation_id: string, project_id: string) =>
    post("/conversation/move", { conversation_id, project_id }),
  listProjects: () => post<Project[]>("/project/list", {}),
  getPersonal: () => post<Personalization>("/personal/get", {}),
  savePersonal: (p: Personalization) => post<Personalization>("/personal/save", p),
  listMemories: () => post<Memory[]>("/memory/list", {}),
  deleteMemory: (memory_id: string) => post("/memory/delete", { memory_id }),
  saveProject: (p: { project_id?: string; name: string; description?: string; instructions?: string; default_skill?: string }) =>
    post<Project>("/project/save", p),
  deleteProject: (project_id: string) => post("/project/delete", { project_id }),
  renameConversation: (conversation_id: string, title: string) =>
    post("/conversation/rename", { conversation_id, title }),
  deleteConversation: (conversation_id: string) =>
    post("/conversation/delete", { conversation_id }),
  pruneConversations: () => post<{ removed: number }>("/conversation/prune-empty", {}),
  shareConversation: (conversation_id: string, email: string, role: "viewer" | "editor" | "none") =>
    post<{ shares: { email: string; role: string }[] }>("/conversation/share", { conversation_id, email, role }),
  sharedWithMe: () => post<Conversation[]>("/conversation/shared-with-me", {}),
  getMessages: (conversation_id: string) =>
    post<Message[]>("/conversation/get-messages", { conversation_id, size: 200 }),
  searchMessages: (query: string, limit = 30) =>
    post<{ hits: MessageSearchHit[] }>("/conversation/search", { query, limit }),
  forkConversation: (conversation_id: string, message_id: string) =>
    post<Conversation>("/conversation/fork", { conversation_id, message_id }),
  getTasks: (filter: { conversation_id?: string } = {}) =>
    post<{ tasks: TaskMeta[]; owners: Record<string, OwnerInfo> }>("/task/get-tasks", { ...filter, size: 100 }),
  // silent: the task editor shows validation errors inline instead of a toast.
  listConnectors: () => post<{ connectors: Connector[] }>("/connector/list", {}),
  oauthProviders: () => post<OAuthProvider[]>("/connector/oauth/providers", {}),
  startConnectorOAuth: (p: { provider: string; mcp_url?: string; connector_id?: string }) => post<{ auth_url: string }>("/connector/oauth/start", p),
  testConnector: (c: ConnectorInput) => post<{ ok: boolean; tools?: string[]; error?: string }>("/connector/test", c, true),
  createConnector: (c: ConnectorInput) => post<Connector>("/connector/create", c, true),
  deleteConnector: (connector_id: string) => post("/connector/delete", { connector_id }),
  systemStatus: () => get<SystemStatus>("/system/status"),
  saveTask: (task: TaskInput) => post<TaskMeta>("/task/save", task, true),
  toggleTask: (task_id: string, enabled: boolean) => post<TaskMeta>("/task/toggle", { task_id, enabled }),
  runTaskNow: (task_id: string) => post<{ status: string; conversation_id: string }>("/task/run-now", { task_id }),
  deleteTask: (task_id: string) => post("/task/delete", { task_id }),
  listRuns: (filter: { conversation_id?: string; task_id?: string; status?: string; statuses?: string[]; offset?: number; size?: number } = {}) =>
    post<{ runs: RunRecord[]; has_more?: boolean }>("/run/list", { size: 50, ...filter }),
  getRun: (run_id: string) => post<RunRecord>("/run/get", { run_id }),
  rerunRun: (run_id: string) => post<{ status: string }>("/run/rerun", { run_id }),
  // Continue a run that died mid-flight from its surviving transcript, rather
  // than paying for the completed work a second time.
  resumeRun: (run_id: string) =>
    post<{ resumed: boolean; conversation_id: string; steps: number }>("/chat/resume_run", { run_id }),
  // Stop a continuation the control plane scheduled after a provider outage.
  // The run stays resumable by hand.
  cancelAutoResume: (run_id: string) =>
    post<{ cancelled: boolean; run_id: string }>("/chat/cancel_auto_resume", { run_id }),
  listNotifications: () => post<{ notifications: Notification[]; unread: number }>("/notification/list", {}),
  readNotifications: (notification_id = "") => post("/notification/read", { notification_id }),
  followups: (prompt: string, answer: string, selectedVersion = "auto", modelProfile = "", conversationID = "", runID = "") => post<{ suggestions: string[] }>("/chat/followups", { prompt, answer, selected_version: selectedVersion, model_profile: modelProfile, conversation_id: conversationID, run_id: runID }, true),
  listSkills: () => post<{ skills: SkillSummary[] }>("/skill/list", {}),
  previewSkill: (url: string) => post<{ name: string; description: string; prompt: string }>("/skill/preview", { url }),
  enableSkill: (name: string, enabled: boolean) => post("/skill/enable", { name, enabled }),
  getSkill: (name: string) => post<{ name: string; description: string; prompt: string }>("/skill/get", { name }),
  installSkill: (url: string) => post<{ name: string }>("/skill/install", { url }),
  deleteSkill: (name: string) => post("/skill/delete", { name }),
  runBudget: (run_id: string) => get<import("./lib/runBudget").RunBudget>(`/run/${encodeURIComponent(run_id)}/budget`),
  runAcceptance: (run_id: string) => post<import('./lib/runEvidence').RunAcceptance>("/run/acceptance", { run_id }),
  listDeliveries: (conversation_id: string) => post<{ deliveries: import('./lib/runEvidence').DeliverySnapshot[] }>("/delivery/list", { conversation_id }),
  deliveryDownloadURL: (conversation_id: string, run_id: string, path: string) => `${BASE}/delivery/download?${new URLSearchParams({ conversation_id, run_id, path, token: auth.token() })}`,
  metrics: async (): Promise<MetricsSnapshot> => {
    const res = await fetch(BASE + "/metrics", { headers: headers(false) });
    const j = await res.json();
    return j.data as MetricsSnapshot;
  },
  models: async (): Promise<{ version: string; label: string; hint: string }[]> => {
    const res = await fetch(BASE + "/models", { headers: headers(false) });
    const j = await res.json();
    return (j.data ?? []) as { version: string; label: string; hint: string }[];
  },
  kill: (conversationID: string) => post("/chat/kill", { conversation_id: conversationID }),
  // active: is a run live for this conversation right now? Asked on open, so a
  // run this tab did not start (refresh, second tab, schedule, API) is rejoined
  // instead of shown as a frozen snapshot.
  active: (id: string) =>
    get<{ active: boolean; seq: number }>(`/chat/active?conversation_id=${encodeURIComponent(id)}`).catch(() => ({ active: false, seq: 0 })),
};



// The endpoint, key and model list, editable without touching a file. The key
// is write-only: it is never returned, and omitting it on save keeps the stored
// one — so changing a URL never requires re-typing it.

export type FileVersion = { ts: string; when: number; size: number; path: string };

export type FileEntry = { name: string; dir: boolean; size: number; mtime?: number };
export interface SkillSummary { name: string; description: string; enabled?: boolean; builtin?: boolean }

// File calls take a scope: a conversation id, or projectScope(id) for a
// project's shared files.
export const projectScope = (projectID: string) => "project:" + projectID;
function fileScope(scope: string): Record<string, string> {
  if (!scope.trim()) throw new Error("请选择对话后再操作文件");
  if (scope.startsWith("project:")) { const id = scope.slice("project:".length); return { project_id: id, proj: id }; }
  return { conversation_id: scope, conv: scope };
}
function fileQuery(conversationID: string) {
  return "&" + new URLSearchParams(fileScope(conversationID)).toString();
}

export const files = {
  list: (path: string, conversationID: string, silent = false) =>
    post<FileEntry[]>("/file/list", { path, ...fileScope(conversationID) }, silent),
  scopedList: (path: string, conversation_id: string, silent = false) =>
    post<FileEntry[]>("/file/list", { path, ...fileScope(conversation_id) }, silent),
  delete: (path: string, conversation_id: string) => post("/file/delete", { path, ...fileScope(conversation_id) }),
  versions: (path: string, conversation_id: string) => post<FileVersion[]>("/file/versions", { path, ...fileScope(conversation_id) }),
  restore: (path: string, ts: string, conversation_id: string) => post<{ restored: string }>("/file/restore", { path, ts, ...fileScope(conversation_id) }),
  // Every file URL is scoped to the selected conversation, including owner views.
  downloadURL: (path: string, conv: string) =>
    `${BASE}/file/download?path=${encodeURIComponent(path)}&token=${encodeURIComponent(auth.token())}${fileQuery(conv)}`,
  // previewURL serves the same bytes with Content-Disposition: inline so the
  // browser renders them in-page (PDF in an <iframe>) instead of downloading.
  previewURL: (path: string, conv: string) =>
    `${BASE}/file/download?path=${encodeURIComponent(path)}&token=${encodeURIComponent(auth.token())}&inline=1${fileQuery(conv)}`,
  upload: async (file: File, dir: string, onProgress: ((pct: number) => void) | undefined, conversation_id: string) => {
    fileScope(conversation_id);
    const CHUNK = 256 * 1024;
    const total = Math.max(1, Math.ceil(file.size / CHUNK));
    const uploadID = crypto.randomUUID();
    const filename = (dir ? dir.replace(/\/$/, "") + "/" : "") + file.name;
    for (let i = 0; i < total; i++) {
      const b64 = await blobToB64(file.slice(i * CHUNK, (i + 1) * CHUNK));
      await post("/file/upload-chunk", { upload_id: uploadID, filename, index: i, total, data: b64, ...fileScope(conversation_id) });
      onProgress?.(Math.round(((i + 1) / total) * 100));
    }
    return filename;
  },
};

function blobToB64(b: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve((r.result as string).split(",")[1] ?? "");
    r.onerror = reject;
    r.readAsDataURL(b);
  });
}

export interface ModelSettingsData {
  policies?: Record<string,import("./lib/modelPolicies").ModelPolicy>;
  provider: string;
  base_url: string;
  api_key_set: boolean;
  models: string[];
  enabled: boolean;
}
export type ModelSettingsInput = Omit<ModelSettingsData, "api_key_set"> & { api_key?: string };
export const modelSettings = {
  get: () => post<ModelSettingsData>("/model-settings/get", {}),
  save: (data: ModelSettingsInput) => post<ModelSettingsData>("/model-settings/save", data),
  discover: (data: { provider: string; base_url: string; api_key?: string }) =>
    post<{ models: string[]; source?: "remote" | "preset"; notice?: string }>("/model-settings/discover", data, true),
};

export const modelProfiles = {
 get: () => post<import('./lib/modelProfiles').ModelProfiles>('/model-profiles/get', {}),
 save: (data: {active_profile_id:string;profiles:ReturnType<typeof import('./lib/modelProfiles').profileInput>[]}) => post<import('./lib/modelProfiles').ModelProfiles>('/model-profiles/save', data),
 discover: (data: {profile_id:string;protocol:string;provider:string;base_url:string;api_key?:string}) => post<{models:string[];source?:string;notice?:string}>('/model-settings/discover', data, true),
 probe: (data: {profile_id:string;model:string;capabilities:import('./lib/modelProfiles').Capability[]}) => post<import('./lib/modelProfiles').ModelVerification>('/model-profiles/probe', data, true),
};

