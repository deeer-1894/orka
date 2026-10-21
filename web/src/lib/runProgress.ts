import type { Message, PlanPayload, PlanStep, ToolPayload } from '../types';

// Everything a long run shows about its own progress — the pinned bar, the
// steps folded per plan item, the closing delivery card — is derived here from
// the event stream the thread already holds (plan snapshots, tool calls, task
// status). Nothing new is asked of the backend.

export interface RunProgress {
  runID: string;
  total: number;        // plan steps
  done: number;
  current?: { index: number; title: string };
  toolCalls: number;
  startedAt: number;    // unix millis
  agents: string[];     // sub-agents that have acted in this run
}

export interface StepGroup {
  key: string;
  index: number;        // plan position, -1 before the plan existed
  title: string;
  items: Message[];
}

export interface DeliverySummary {
  status: 'done' | 'partial' | 'failed' | 'stopped';
  done: number;
  total: number;
  unfinished: { title: string; reason?: string }[];
  outputs: string[];
}

const TERMINAL = new Set(['done', 'partial', 'failed', 'stopped', 'killed']);

// lastRunID is the run the newest message belongs to.
export function lastRunID(messages: Message[]): string {
  for (let i = messages.length - 1; i >= 0; i--) {
    const id = messages[i].meta?.run_id;
    if (id) return id;
  }
  return '';
}

function planSteps(m: Message): PlanStep[] {
  return ((m.payload as PlanPayload | undefined)?.steps) || [];
}

// focusIndex is the step work is attributed to: the active one, else the first
// that is neither done nor blocked.
export function focusIndex(steps: PlanStep[]): number {
  const active = steps.findIndex(s => s.status === 'active');
  if (active >= 0) return active;
  return steps.findIndex(s => s.status !== 'done' && s.status !== 'blocked');
}

export function runProgress(messages: Message[], runID = lastRunID(messages)): RunProgress | undefined {
  if (!runID) return undefined;
  const run = messages.filter(m => m.meta?.run_id === runID);
  if (run.length === 0) return undefined;
  let steps: PlanStep[] = [];
  for (const m of run) if (m.type === 'plan') steps = planSteps(m);
  const start = run.find(m => m.type === 'task' && m.action === 'start') || run[0];
  const index = focusIndex(steps);
  return {
    runID,
    total: steps.length,
    done: steps.filter(s => s.status === 'done').length,
    current: index >= 0 ? { index, title: steps[index].title } : undefined,
    toolCalls: run.filter(m => m.type === 'tool').length,
    startedAt: start.ts,
    agents: [...new Set(run.map(m => m.meta?.agent_id).filter((a): a is string => !!a))],
  };
}

// stepGroups files each step message under the plan item that was in focus
// when it happened. Plan snapshots arrive interleaved with the tool calls, so
// a walk over the whole stream sees the plan exactly as it stood at each call.
export function stepGroups(messages: Message[], items: Message[]): StepGroup[] {
  const wanted = new Set(items.map(m => m.id));
  const plans = new Map<string, PlanStep[]>();
  const groups: StepGroup[] = [];
  const byKey = new Map<string, StepGroup>();
  for (const m of messages) {
    const run = m.meta?.run_id || '';
    if (m.type === 'plan') { plans.set(run, planSteps(m)); continue; }
    if (!wanted.has(m.id)) continue;
    const steps = plans.get(run) || [];
    const index = focusIndex(steps);
    const title = index >= 0 ? steps[index].title : '准备';
    const key = run + '\u0000' + title;
    let g = byKey.get(key);
    if (!g) {
      g = { key, index, title, items: [] };
      byKey.set(key, g);
      groups.push(g);
    }
    g.items.push(m);
  }
  return groups;
}

// What a tool call counts as in a one-line summary of a step.
const NOUN: Record<string, [string, string]> = {
  web_search: ['搜索', '次'], search_evidence: ['检索资料', '次'], fetch_url: ['读取网页', '个'], read_section: ['阅读章节', '个'],
  discover_docs: ['查找文档', '次'], browser: ['浏览器操作', '次'], python: ['运行代码', '次'], shell: ['执行命令', '次'],
  file_write: ['写入文件', '个'], file_edit: ['修改文件', '个'], file_read: ['读取文件', '个'], file_list: ['查看目录', '次'],
  http_request: ['网络请求', '次'], run_agent: ['图形界面操作', '次'],
};

export function stepSummary(items: Message[]): string {
  const counts = new Map<string, { noun: string; unit: string; n: number }>();
  let other = 0;
  for (const m of items) {
    if (m.type !== 'tool') continue;
    const tool = (m.payload as ToolPayload | undefined)?.tool || '';
    const noun = NOUN[tool];
    if (!noun) { other++; continue; }
    const c = counts.get(tool) || { noun: noun[0], unit: noun[1], n: 0 };
    c.n++;
    counts.set(tool, c);
  }
  const parts = [...counts.values()].map(c => `${c.noun} ${c.n} ${c.unit}`);
  if (other) parts.push(`其他工具 ${other} 次`);
  return parts.join(' · ');
}

// deliverySummary describes a finished run: how far its plan got, what is
// left and why, and which files it said it would deliver. undefined while the
// run is still going or never made a plan.
export function deliverySummary(messages: Message[], runID = lastRunID(messages)): DeliverySummary | undefined {
  const run = messages.filter(m => m.meta?.run_id === runID);
  const end = [...run].reverse().find(m => m.type === 'task' && TERMINAL.has(m.action || ''));
  const plan = [...run].reverse().find(m => m.type === 'plan');
  if (!end || !plan) return undefined;
  const steps = planSteps(plan);
  if (steps.length === 0) return undefined;
  const action = end.action === 'killed' ? 'stopped' : end.action as DeliverySummary['status'];
  return {
    status: action,
    done: steps.filter(s => s.status === 'done').length,
    total: steps.length,
    unfinished: steps.filter(s => s.status !== 'done').map(s => ({ title: s.title, reason: s.reason })),
    outputs: (plan.payload as PlanPayload).outputs || [],
  };
}

// elapsedLabel renders a run's age as 6 分 12 秒 / 1 小时 3 分.
export function elapsedLabel(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s} 秒`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} 分 ${s % 60} 秒`;
  return `${Math.floor(m / 60)} 小时 ${m % 60} 分`;
}
