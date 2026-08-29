import type {
  Artifact,
  ArtifactVersion,
  Connector,
  Conversation,
  Factor,
  MessageSearchHit,
  MetricsSnapshot,
  Message,
  Notification,
  OwnerInfo,
  RunRecord,
  TaskMeta,
  WeightedPortfolio,
  Workflow,
  WorkflowStep,
} from "./types";
import { toastError } from "./lib/toast";

const BASE = "/api/v1/controller";
const TOKEN_KEY = "orka.token";

export const auth = {
  token: () => localStorage.getItem(TOKEN_KEY) || "",
  set: (t: string) => localStorage.setItem(TOKEN_KEY, t),
  clear: () => localStorage.removeItem(TOKEN_KEY),
};

let unauthorizedHandler: (() => void) | null = null;
export function setOnUnauthorized(fn: () => void) {
  unauthorizedHandler = fn;
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
    // token missing/expired — clear it and let the app fall back to the login
    // screen on its next render (no jarring full-page reload).
    auth.clear();
    unauthorizedHandler?.();
    toastError("登录已过期，请重新登录");
    throw new Error("unauthorized");
  }
  const j = await res.json().catch(() => ({}));
  if (j && typeof j.code === "number" && j.code !== 0) {
    if (!silent) toastError(j.msg || `请求失败 (${res.status})`);
    throw new Error(j.msg || "request failed");
  }
  if (res.status >= 500) {
    if (!silent) toastError("服务暂时不可用，请稍后再试");
    throw new Error("server " + res.status);
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
    auth.clear();
    unauthorizedHandler?.();
    throw new Error("unauthorized");
  }
  const j = await res.json().catch(() => ({}));
  return (j.data ?? j) as T;
}

export type ToolInfo = { name: string; description: string; group: string; danger: boolean };

export const tools = {
  // Available tools (with descriptions + groups) — drives the tool picker.
  catalog: () => get<ToolInfo[]>("/tools/catalog"),
};

export const chat = {
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

export const api = {
  me: () => post<{ email: string; name: string }>("/auth/me", {}),
  listConversations: () => post<Conversation[]>("/conversation/list", {}),
  createConversation: (title: string) =>
    post<Conversation>("/conversation/create-conversation", { title }),
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
  scheduleTask: (prompt: string, interval_sec: number, title: string, conversation_id = "", retry_count = 0) =>
    post<TaskMeta>("/task/schedule", { prompt, interval_sec, title, conversation_id, retry_count }),
  unscheduleTask: (task_id: string) => post("/task/unschedule", { task_id }),
  enableWebhook: (task_id: string) => post<{ token: string; path: string }>("/task/webhook/enable", { task_id }),
  disableWebhook: (task_id: string) => post("/task/webhook/disable", { task_id }),
  listRuns: (filter: { conversation_id?: string; status?: string } = {}) =>
    post<{ runs: RunRecord[] }>("/run/list", { ...filter, size: 50 }),
  rerunRun: (run_id: string) => post<{ status: string }>("/run/rerun", { run_id }),
  listFactors: (status = "") => post<{ factors: Factor[] }>("/quant/factors", { status }),
  setFactorStatus: (factor_id: string, status: string) => post<{ status: string }>("/quant/factor/status", { factor_id, status }),
  listPortfolios: () => post<{ portfolios: WeightedPortfolio[] }>("/quant/portfolios", {}),
  runFactorPipeline: () => post<{ started: number; reports: string[] }>("/quant/pipeline/run", {}),
  listConnectors: () => post<{ connectors: Connector[] }>("/connector/list", {}),
  testConnector: (c: Partial<Connector>) => post<{ ok: boolean; tools?: string[]; error?: string }>("/connector/test", c),
  createConnector: (c: Partial<Connector>) => post<Connector>("/connector/create", c),
  deleteConnector: (connector_id: string) => post("/connector/delete", { connector_id }),
  listNotifications: () => post<{ notifications: Notification[]; unread: number }>("/notification/list", {}),
  readNotifications: (notification_id = "") => post("/notification/read", { notification_id }),
  listWorkflows: () => post<{ workflows: Workflow[] }>("/workflow/list", {}),
  createWorkflow: (name: string, steps: WorkflowStep[]) => post<Workflow>("/workflow/create", { name, steps }),
  deleteWorkflow: (workflow_id: string) => post("/workflow/delete", { workflow_id }),
  runWorkflow: (workflow_id: string) => post<{ conversation_id: string }>("/workflow/run", { workflow_id }),
  followups: (prompt: string, answer: string) => post<{ suggestions: string[] }>("/chat/followups", { prompt, answer }),
  listSkills: () => post<{ skills: { name: string; description: string }[] }>("/skill/list", {}),
  getSkill: (name: string) => post<{ name: string; description: string; prompt: string }>("/skill/get", { name }),
  installSkill: (url: string) => post<{ name: string }>("/skill/install", { url }),
  deleteSkill: (name: string) => post("/skill/delete", { name }),
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
  kill: (id: string) => post("/chat/kill", { conversation_id: id, task_id: id }),
};

export type FileVersion = { ts: string; when: number; size: number; path: string };

export const files = {
  list: (path: string) =>
    post<{ name: string; dir: boolean; size: number }[]>("/file/list", { path }),
  delete: (path: string) => post("/file/delete", { path }),
  versions: (path: string) => post<FileVersion[]>("/file/versions", { path }),
  restore: (path: string, ts: string) => post<{ restored: string }>("/file/restore", { path, ts }),
  // conv (optional) reads the file from a shared conversation's OWNER workspace.
  downloadURL: (path: string, conv?: string) =>
    `${BASE}/file/download?path=${encodeURIComponent(path)}&token=${encodeURIComponent(auth.token())}${conv ? "&conv=" + encodeURIComponent(conv) : ""}`,
  // previewURL serves the same bytes with Content-Disposition: inline so the
  // browser renders them in-page (PDF in an <iframe>) instead of downloading.
  previewURL: (path: string, conv?: string) =>
    `${BASE}/file/download?path=${encodeURIComponent(path)}&token=${encodeURIComponent(auth.token())}&inline=1${conv ? "&conv=" + encodeURIComponent(conv) : ""}`,
  upload: async (file: File, dir: string, onProgress?: (pct: number) => void) => {
    const CHUNK = 256 * 1024;
    const total = Math.max(1, Math.ceil(file.size / CHUNK));
    const uploadID = crypto.randomUUID();
    const filename = (dir ? dir.replace(/\/$/, "") + "/" : "") + file.name;
    for (let i = 0; i < total; i++) {
      const b64 = await blobToB64(file.slice(i * CHUNK, (i + 1) * CHUNK));
      await post("/file/upload-chunk", { upload_id: uploadID, filename, index: i, total, data: b64 });
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
