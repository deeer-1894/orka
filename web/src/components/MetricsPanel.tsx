import { BudgetEvidence } from './BudgetEvidence';
import type { Message, RunRecord } from '../types';
export interface MetricsRunContext { conversationID: string; messages: Message[]; run?: RunRecord | null }
import { api } from "../api";
import { useResource } from "../lib/useResource";
export function MetricsPanel({ runContext }: { runContext?: MetricsRunContext }) {
  const ownRun = runContext?.run?.conversation_id === runContext?.conversationID ? runContext?.run : undefined;
  const ownMessages = runContext?.messages.filter(message => message.meta?.conversation_id === runContext.conversationID) || [];
  const runID = [...ownMessages].reverse().find(message => message.meta?.run_id)?.meta.run_id || ownRun?.run_id;
  const run = ownRun?.run_id === runID ? ownRun : undefined;
  const model = [...ownMessages].reverse().find(message => message.meta?.run_id === runID && message.meta?.model_version)?.meta.model_version;
  // Overview composes this section; one shared resource owns live usage counters.
  const m = useResource("metrics", api.metrics, { interval: 4000 }) ?? null;
  const fmt = (n: number) => (n >= 1000 ? (n / 1000).toFixed(1) + "k" : String(n));
  const stats = [
    { k: "运行中会话", v: m?.active_sessions ?? 0 },
    { k: "Checkpoints", v: m?.checkpoints ?? 0 },
    { k: "工具调用", v: m?.tool_calls ?? 0 },
    { k: "平均工具 µs", v: Math.round(m?.avg_tool_call_micros ?? 0) },
    { k: "LLM 调用", v: m?.llm_calls ?? 0 },
    { k: "总 tokens", v: fmt(m?.total_tokens ?? 0) },
    { k: "输入 tokens", v: fmt(m?.prompt_tokens ?? 0) },
    { k: "输出 tokens", v: fmt(m?.completion_tokens ?? 0) },
  ];
  return (
    <div className="flex flex-col gap-3">
      {runID && <section aria-label="当前运行统计" className="space-y-2 rounded-xl border border-border p-3 text-xs">
        <strong>当前会话运行</strong><p className="break-all text-muted">{runID}</p>
        {model && <p>模型：{model}</p>}
        {run && <p>{run.tokens || 0} tokens · {run.tool_calls || 0} 次工具调用</p>}
        <BudgetEvidence key={runID} runID={runID}/>
      </section>}
      <p className="text-xs text-muted">累计指标</p>
      <div className="grid grid-cols-2 gap-2.5">
      {stats.map((s) => (
        <div key={s.k} className="rounded-xl border border-border bg-surface2/50 p-3.5">
          <div className="font-serif text-[26px] text-ink">{s.v}</div>
          <div className="mt-1 text-[12px] text-faint">{s.k}</div>
        </div>
      ))}
      </div>
    </div>
  );
}
