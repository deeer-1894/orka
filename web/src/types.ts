// Mirrors the Go event protocol in orka_core/messages.

export type EventType =
  | "task"
  | "chat"
  | "clarify"
  | "confirm"
  | "file"
  | "skill"
  | "agent"
  | "tool"
  | "browser"
  | "heartbeat"
  | "plan"
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

export interface ConfirmPayload {
  id: string;
  tool: string;
  summary: string;
}

export type PlanStepStatus = "pending" | "active" | "done";

export interface PlanStep {
  title: string;
  status: PlanStepStatus;
}

export interface PlanPayload {
  steps: PlanStep[];
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

export interface ConversationShare {
  email: string;
  role: "viewer" | "editor";
}

export interface Conversation {
  conversation_id: string;
  title: string;
  task_ids: string[];
  created_at: number;
  owner_email?: string;
  shares?: ConversationShare[];
  parent_conversation_id?: string; // set when this conversation is a branch fork
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
  webhook_token?: string;
  retry_count?: number;
}

export interface FactorMetrics {
  ic: number;
  ir: number;
  sharpe: number;
  turnover: number;
  max_dd: number;
  periods: number;
}

export interface Factor {
  factor_id: string;
  name: string;
  source_report_id?: string;
  rationale: string;
  expression: string;
  direction: string; // long | short | long_short
  universe?: string;
  horizon?: string;
  status: string; // proposed | backtested | approved | rejected | live
  agreement_score?: number;
  metrics: FactorMetrics;
  created_at: number;
}

export interface WeightedPortfolio {
  portfolio_id: string;
  method: string;
  factor_ids: string[];
  weights: number[];
  metrics: FactorMetrics;
  created_at: number;
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
  result?: string; // structured JSON extracted from the answer
  error: string;
  tool_calls: number;
  tokens: number;
  trace_id: string;
  created_at: number;
  finished_at: number;
  duration_ms: number;
}

export type ArtifactBlock = { type: string; data: Record<string, unknown> };
export interface ArtifactVersion {
  artifact_id: string;
  version: number;
  blocks: ArtifactBlock[];
  note?: string;
  created_at: number;
}
export interface Artifact {
  artifact_id: string;
  owner_email: string;
  conversation_id: string;
  title: string;
  kind: string;
  slug: string;
  visibility: "private" | "shared" | "public";
  shares?: ConversationShare[];
  share_token?: string;
  current_version: number;
  created_at: number;
  updated_at: number;
}

export interface WorkflowStep {
  name: string;
  prompt: string;
  depends_on?: string[];
  run_if?: string;
  on_error?: string; // stop | continue | retry:N
}
export interface Workflow {
  workflow_id: string;
  name: string;
  steps: WorkflowStep[];
  created_at: number;
}

export interface MessageSearchHit {
  conversation_id: string;
  title: string;
  snippet: string;
  role: string;
  created_at: number;
}

export interface Notification {
  notification_id: string;
  kind: string;
  title: string;
  body: string;
  run_id: string;
  conversation_id: string;
  read: boolean;
  created_at: number;
}

export interface Connector {
  connector_id: string;
  name: string;
  transport: string; // http | streamable_http | stdio
  url: string;
  command: string;
  args: string[];
  headers: Record<string, string>;
  enabled: boolean;
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
  llm_calls: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
}
