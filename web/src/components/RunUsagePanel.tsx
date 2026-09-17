import { ActionChip, actionChipClass } from './ActionChip';
import type { useRunUsage } from '../hooks/useRunUsage';
const quantity = (value: number | undefined) => typeof value === 'number' && Number.isFinite(value) ? String(value) : '未知';
export default function RunUsagePanel({
  data, error, refresh
}: ReturnType<typeof useRunUsage>) {
  return <section aria-label="运行用量" className="space-y-2 rounded-lg border border-border p-3 text-xs">
    <div className="flex justify-between"><strong>运行用量</strong><ActionChip onClick={refresh} icon="refresh">刷新用量</ActionChip></div>
    {error && <p role="alert" className="text-accent">用量读取失败：{error}</p>}
    {!data && !error && <p role="status">正在读取用量…</p>}
    {data && <>
      {data.budget_run_id !== data.run_id && <p className="break-all">共享运行：{data.budget_run_id}</p>}
      <p>已用（含保守占用）{quantity(data.used_tokens)} · 预留 {quantity(data.reserved_tokens)} tokens</p>
      <p>未知用量：{quantity(data.unknown_calls)} 次调用，保守占用 {quantity(data.unknown_tokens)} tokens</p>
      {!!data.unknown_calls && <p className="text-muted">已用已包含未知调用的保守占用，不重复相加；预留是额外的在途占用。</p>}
      <p>估算用量：{quantity(data.estimated_tokens)} tokens</p>
      <p>已用轮次：{quantity(data.used_steps)}</p>
      <details><summary className={actionChipClass + " cursor-pointer list-none"}>按调用来源查看</summary><ul className="space-y-2 pt-2">{(data.sources || []).map(source => <li key={source.source}><strong>{source.source}</strong><p>已用（含保守占用）{quantity(source.used_tokens)} · 预留 {quantity(source.reserved_tokens)} · 估算 {quantity(source.estimated_tokens)}</p><p>未知 {quantity(source.unknown_calls)} 次 · 保守占用 {quantity(source.unknown_tokens)} tokens</p></li>)}</ul></details>
    </>}
  </section>;
}
