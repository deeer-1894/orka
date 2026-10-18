import { useRunUsage } from '../hooks/useRunUsage';
import { ActionChip } from './ActionChip';
import { lazy, Suspense, useState } from 'react';
const RunUsagePanel = lazy(() => import('./RunUsagePanel'));
export function UsageEvidence({
  runID, summary = false
}: {
  runID: string;
  summary?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const usage = useRunUsage(runID, summary || open);
  return <div className="space-y-2">
    {summary && usage.data && <p>累计用量 {usage.data.used_tokens} tokens（含未知调用的保守占用） · 在途 {usage.data.reserved_tokens}</p>}
    {summary && !usage.data && <p>{usage.error ? "用量暂不可用" : "正在读取用量…"}</p>}
    <ActionChip aria-expanded={open} onClick={() => setOpen(value => !value)} icon="coin">用量明细</ActionChip>
    {open && <Suspense fallback={<p role="status">正在加载用量…</p>}><RunUsagePanel {...usage} /></Suspense>}
  </div>;
}
