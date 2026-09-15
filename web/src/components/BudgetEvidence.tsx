import { ActionChip } from './ActionChip';
import { lazy, Suspense, useState } from 'react';
const RunBudgetPanel = lazy(() => import('./RunBudgetPanel'));
export function BudgetEvidence({
  runID
}: {
  runID: string;
}) {
  const [open, setOpen] = useState(false);
  return <div className="space-y-2"><ActionChip aria-expanded={open} onClick={() => setOpen(value => !value)} icon="coin">预算明细</ActionChip>
    {open && <Suspense fallback={<p role="status">正在加载预算…</p>}><RunBudgetPanel key={runID} runID={runID} /></Suspense>}
  </div>;
}
