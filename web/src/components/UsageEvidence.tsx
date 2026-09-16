import { ActionChip } from './ActionChip';
import { lazy, Suspense, useState } from 'react';
const RunUsagePanel = lazy(() => import('./RunUsagePanel'));
export function UsageEvidence({
  runID
}: {
  runID: string;
}) {
  const [open, setOpen] = useState(false);
  return <div className="space-y-2"><ActionChip aria-expanded={open} onClick={() => setOpen(value => !value)} icon="coin">用量明细</ActionChip>
    {open && <Suspense fallback={<p role="status">正在加载用量…</p>}><RunUsagePanel key={runID} runID={runID} /></Suspense>}
  </div>;
}
