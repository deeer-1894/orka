import { ActionChip } from './ActionChip';
import { AcceptanceEvidence } from './AcceptanceEvidence';
import { useState } from 'react';
import { api } from '../api';
import type { RunRecord } from '../types';
import { resumableRun } from '../lib/runActions';
import { useRunHistory } from '../hooks/useRunHistory';
const labels: Record<string, string> = {
  running: '运行中',
  done: '已完成',
  partial: '部分完成',
  failed: '失败',
  interrupted: '已中断',
  paused: '等待确认'
};
const triggers: Record<string, string> = {
  manual: '手动',
  schedule: '定时',
  condition: '条件触发',
  webhook: 'Webhook',
  rerun: '重新执行',
  resume: '继续',
  workflow: '流程'
};
export interface RunsPanelProps {
  onJumpToConversation: (cid: string) => void;
  onResumeRun: (run: RunRecord) => Promise<unknown>;
  isRunBusy: (cid: string) => boolean;
  runRevision: number;
  canResumeRun: (run: RunRecord) => boolean;
}
export default function RunsPanel({
  onJumpToConversation,
  onResumeRun,
  isRunBusy,
  runRevision,
  canResumeRun
}: RunsPanelProps) {
  const [attention, setAttention] = useState(false);
  const history = useRunHistory(runRevision, attention);
  const [error, setError] = useState(''),
    [pending, setPending] = useState<string | null>(null);
  const execute = async (run: RunRecord, resume: boolean) => {
    if (pending || isRunBusy(run.conversation_id)) return;
    setPending(run.run_id);
    setError('');
    try {
      if (resume) await onResumeRun(run);else await api.rerunRun(run.run_id);
      history.refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : '操作失败，请重试');
    } finally {
      setPending(null);
    }
  };
  return <div className="space-y-3 p-3">
  <div className="flex flex-wrap items-center justify-end gap-2"><ActionChip aria-pressed={attention} onClick={() => {
        setAttention(v => !v);
        history.setPage(0);
      }} icon="gear">需要处理</ActionChip><ActionChip onClick={history.refresh} icon="refresh">刷新运行记录</ActionChip></div>
  {(error || history.error) && <p role="alert" className="text-sm text-accent">{error || history.error}</p>}
  {history.loading && !history.runs.length && <p role="status" className="text-sm text-muted">正在读取运行记录…</p>}
  {!history.loading && !history.error && !history.runs.length && <p className="text-sm text-muted">{attention ? '当前查询范围没有需要处理的记录' : '当前页没有运行记录'}</p>}
  {history.runs.map(r => <article key={r.run_id} className="space-y-2 rounded-xl border border-border p-3">
   <div className="flex justify-between text-xs text-muted"><span>{labels[r.status] || r.status}{r.trigger && r.trigger !== 'manual' ? ` · ${triggers[r.trigger] || r.trigger}` : ''}</span><time>{new Date(r.created_at).toLocaleString()}</time></div>
   <p className="line-clamp-2 text-sm" title={r.prompt}>{r.prompt}</p>{r.error && <p className="line-clamp-2 text-xs text-accent">{r.error}</p>}
   {!!r.unfinished?.length && <p className="text-xs text-muted">未完成：{r.unfinished.join(' · ')}</p>}
   {r.budget_hit && <p className="text-xs text-muted">历史停止原因：{r.budget_hit === 'tokens' ? '旧 Token 限额（已取消）' : r.budget_hit}</p>}
   <p className="text-xs text-faint">{r.status === "running" ? "执行中" : `${Math.round((r.duration_ms || 0) / 1000)} 秒 · ${r.tokens || 0} tokens · ${r.tool_calls || 0} 次工具调用`}</p>
   <div className="flex flex-wrap gap-3 text-sm"><ActionChip onClick={() => onJumpToConversation(r.conversation_id)} icon="share">查看对话</ActionChip>
   {resumableRun(r) && canResumeRun(r) && <ActionChip disabled={pending !== null || isRunBusy(r.conversation_id)} onClick={() => void execute(r, true)} icon="play">继续任务</ActionChip>}
   {!['running', 'paused'].includes(r.status) && <ActionChip disabled={pending !== null || isRunBusy(r.conversation_id)} onClick={() => void execute(r, false)} icon="refresh">重新执行</ActionChip>}</div>
   <AcceptanceEvidence runID={r.run_id} />
  </article>)}
  <div className="flex justify-between text-sm"><ActionChip disabled={history.page === 0 || history.loading} onClick={() => history.setPage(p => p - 1)}>上一页</ActionChip><ActionChip disabled={!history.hasMore || history.loading} onClick={() => history.setPage(p => p + 1)}>下一页</ActionChip></div>
 </div>;
}
