import { actionChipClass } from './ActionChip';
import { Icon } from './Icon';
import type { RunBudgetLimits } from '../lib/runBudget';
const fields: { key: keyof RunBudgetLimits; label: string }[] = [
  { key: 'max_tokens', label: 'Token 上限' },
  { key: 'max_wall_seconds', label: '运行时长上限（秒）' },
  { key: 'max_steps', label: '轮次上限' },
];
export function BudgetFields({ value, onChange, disabled }: { value: RunBudgetLimits; onChange: (next: RunBudgetLimits) => void; disabled?: boolean }) {
  return <details className="mb-3 text-xs">
    <summary className={actionChipClass + " cursor-pointer list-none"}><Icon name="gear" size={13} />任务预算</summary>
    <p className="my-2 text-muted">用于当前会话的下一次发送，正在运行的任务保持原预算。留空或 0 沿用部署默认长任务额度；超出服务上限时会明确报错。</p>
    <div className="grid gap-2 sm:grid-cols-3">{fields.map(field => <label key={field.key} className="space-y-1">{field.label}
      <input type="number" min="0" step="1" aria-label={field.label} disabled={disabled} value={value[field.key] ?? ''} placeholder="部署默认" onChange={event => onChange({ ...value, [field.key]: event.target.value === '' ? undefined : Number(event.target.value) })} className="w-full rounded border border-border bg-surface p-2" />
    </label>)}</div>
  </details>;
}
