// Mirrors the Go event protocol in orka_core/messages.

export type EventType =
  | "task"
  | "chat"
  | "clarify"
  | "file"
  | "skill"
  | "agent"
  | "tool"
  | "browser"
  | "heartbeat"
  | "stream";

export interface Meta {
  conversation_id: string;
  task_id: string;
  model_version?: string;
  trace_id: string;
  user_email?: string;
  agent_id?: string;
  parent_agent_id?: string;
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

export interface WeatherCardData {
  location: string;
  current: {
    temp_c: string;
    feels_c: string;
    desc: string;
    humidity: string;
    wind_kmph: string;
  };
  forecast?: {
    date: string;
    min_c: string;
    max_c: string;
    desc: string;
    rain: string;
  }[];
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
  interval_sec?: number;
  next_run_at?: number;
  last_result?: string;
}

export interface RunRecord {
  run_id: string;
  task_id: string;
  conversation_id: string;
  owner_email: string;
  trigger: string; // manual | schedule | resume | rerun
  status: string; // running | done | failed | paused
  prompt: string;
  output: string;
  error: string;
  tool_calls: number;
  tokens: number;
  trace_id: string;
  created_at: number;
  finished_at: number;
  duration_ms: number;
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
  llm_calls: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
}
