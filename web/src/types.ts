// Mirrors the Go event protocol in cavis_core/messages.

export type EventType =
  | "task"
  | "chat"
  | "clarify"
  | "file"
  | "skill"
  | "agent"
  | "tool"
  | "browser"
  | "heartbeat";

export interface Meta {
  conversation_id: string;
  task_id: string;
  model_version?: string;
  trace_id: string;
  user_email?: string;
}

export interface Message {
  id: string;
  type: EventType;
  role: string;
  content?: string;
  action?: string;
  payload?: unknown;
  meta: Meta;
  ts: number;
}

export interface ClarifyPayload {
  question: string;
  options?: string[];
  context?: string;
  resume_key: string;
}

export interface ToolPayload {
  tool: string;
  args?: Record<string, unknown>;
  result?: string;
  error?: string;
}

export interface BrowserPayload {
  type?: string;
  mode?: "dom" | "vision" | "macro";
  data?: string; // base64 screenshot
  action?: string;
  target?: string;
  result?: string;
  reason?: string;
  tokens?: number;
}

export interface Conversation {
  conversation_id: string;
  title: string;
  task_ids: string[];
  created_at: number;
}

export interface TaskMeta {
  task_id: string;
  initial_template_id: string;
  cron_status: string;
  run_status: string;
  conversation_id: string;
  owner_email: string;
  variables: Record<string, unknown> | null;
  created_at: number;
}

export interface OwnerInfo {
  email: string;
  name: string;
  avatar: string;
}

export interface MetricsSnapshot {
  active_sessions: number;
  checkpoints: number;
  tool_calls: number;
  avg_tool_call_micros: number;
}
