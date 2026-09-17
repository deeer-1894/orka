import { UsageEvidence } from './UsageEvidence';
import type { Message, RunRecord } from '../types';
export interface MetricsRunContext { conversationID: string; messages: Message[]; run?: RunRecord | null }
export function MetricsPanel({ runContext }: { runContext?: MetricsRunContext }) {
  const ownRun = runContext?.run?.conversation_id === runContext?.conversationID ? runContext?.run : undefined;
  const ownMessages = runContext?.messages.filter(message => message.meta?.conversation_id === runContext.conversationID) || [];
  const runID = [...ownMessages].reverse().find(message => message.meta?.run_id)?.meta.run_id || ownRun?.run_id;
  const model = [...ownMessages].reverse().find(message => message.meta?.run_id === runID && message.meta?.model_version)?.meta.model_version;
  const toolCalls = ownMessages.filter(m => m.meta?.run_id === runID && m.type === 'tool').length;
  return (
    <div className="flex flex-col gap-3">
      {runID && <section aria-label="当前运行统计" className="space-y-2 rounded-xl border border-border p-3 text-xs">
        <strong>当前会话运行</strong><p className="break-all text-muted">{runID}</p>
        {model && <p>模型：{model}</p>}
        <p>当前运行段已记录 {toolCalls} 次工具调用</p>
        <p className="text-xs text-muted">累计指标</p>
        <UsageEvidence key={runID} runID={runID} summary/>
      </section>}
      {!runID && <p className="text-xs text-muted">开始任务后显示当前会话的累计指标。</p>}
    </div>
  );
}
