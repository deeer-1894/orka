import { useRunUsage } from '../hooks/useRunUsage';
import type { Message, RunRecord } from '../types';
export interface MetricsRunContext { conversationID: string; messages: Message[]; run?: RunRecord | null }

// One line for the open conversation's latest run; the run list below holds the rest.
export function MetricsPanel({ runContext }: { runContext?: MetricsRunContext }) {
  const ownRun = runContext?.run?.conversation_id === runContext?.conversationID ? runContext?.run : undefined;
  const ownMessages = runContext?.messages.filter(message => message.meta?.conversation_id === runContext.conversationID) || [];
  const runID = [...ownMessages].reverse().find(message => message.meta?.run_id)?.meta.run_id || ownRun?.run_id || '';
  const toolCalls = ownMessages.filter(m => m.meta?.run_id === runID && m.type === 'tool').length;
  const usage = useRunUsage(runID, !!runID);
  if (!runID) return null;
  const tokens = usage.data ? `${usage.data.used_tokens} tokens` : usage.error ? '用量暂不可用' : '读取用量…';
  return <p aria-label="本对话运行" className="mx-3 mt-3 rounded-lg bg-surface2/60 px-3 py-2 text-xs text-muted">
    本对话最近运行 · {toolCalls} 次工具调用 · {tokens}
  </p>;
}
