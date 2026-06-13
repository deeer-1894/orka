import type {
  Conversation,
  MetricsSnapshot,
  Message,
  OwnerInfo,
  TaskMeta,
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

async function post<T>(path: string, body: unknown): Promise<T> {
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
    toastError(j.msg || `请求失败 (${res.status})`);
    throw new Error(j.msg || "request failed");
  }
  if (res.status >= 500) {
    toastError("服务暂时不可用，请稍后再试");
    throw new Error("server " + res.status);
  }
  return (j.data ?? j) as T;
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
  getMessages: (conversation_id: string) =>
    post<Message[]>("/conversation/get-messages", { conversation_id, size: 200 }),
  getTasks: (filter: { conversation_id?: string } = {}) =>
    post<{ tasks: TaskMeta[]; owners: Record<string, OwnerInfo> }>("/task/get-tasks", { ...filter, size: 100 }),
  scheduleTask: (prompt: string, interval_sec: number, title: string, conversation_id = "") =>
    post<TaskMeta>("/task/schedule", { prompt, interval_sec, title, conversation_id }),
  unscheduleTask: (task_id: string) => post("/task/unschedule", { task_id }),
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

export const files = {
  list: (path: string) =>
    post<{ name: string; dir: boolean; size: number }[]>("/file/list", { path }),
  delete: (path: string) => post("/file/delete", { path }),
  downloadURL: (path: string) =>
    `${BASE}/file/download?path=${encodeURIComponent(path)}&token=${encodeURIComponent(auth.token())}`,
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
