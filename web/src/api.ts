import type {
  Conversation,
  MetricsSnapshot,
  Message,
  OwnerInfo,
  TaskMeta,
} from "./types";

const BASE = "/api/v1/controller";

async function post<T>(path: string, body: unknown, email?: string): Promise<T> {
  const res = await fetch(BASE + path, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(email ? { "X-User-Email": email } : {}) },
    body: JSON.stringify(body),
  });
  const j = await res.json();
  return (j.data ?? j) as T;
}

export const api = {
  createConversation: (title: string) =>
    post<Conversation>("/conversation/create-conversation", { title }),
  getMessages: (conversation_id: string) =>
    post<Message[]>("/conversation/get-messages", { conversation_id, size: 200 }),
  getTasks: (filter: { conversation_id?: string; owner_email?: string } = {}) =>
    post<{ tasks: TaskMeta[]; owners: Record<string, OwnerInfo> }>("/task/get-tasks", { ...filter, size: 100 }),
  createTask: (conversation_id: string, owner_email: string) =>
    post<TaskMeta>("/task/create", { conversation_id, owner_email }),
  metrics: async (): Promise<MetricsSnapshot> => {
    const res = await fetch(BASE + "/metrics");
    const j = await res.json();
    return j.data as MetricsSnapshot;
  },
  kill: (id: string) => post("/chat/kill", { conversation_id: id, task_id: id }),
};

// ---- file API ----
export const files = {
  list: (path: string, email: string) =>
    post<{ name: string; dir: boolean; size: number }[]>("/file/list", { path }, email),
  delete: (path: string, email: string) => post("/file/delete", { path }, email),
  downloadURL: (path: string) => `${BASE}/file/download?path=${encodeURIComponent(path)}`,
  upload: async (file: File, dir: string, email: string, onProgress?: (pct: number) => void) => {
    // chunked, resumable upload
    const CHUNK = 256 * 1024;
    const total = Math.max(1, Math.ceil(file.size / CHUNK));
    const uploadID = crypto.randomUUID();
    const filename = (dir ? dir.replace(/\/$/, "") + "/" : "") + file.name;
    for (let i = 0; i < total; i++) {
      const slice = file.slice(i * CHUNK, (i + 1) * CHUNK);
      const b64 = await blobToB64(slice);
      await post(
        "/file/upload-chunk",
        { upload_id: uploadID, filename, index: i, total, data: b64 },
        email,
      );
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
