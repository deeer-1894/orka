import { ActionChip } from './ActionChip';
import { lazy, Suspense, useState } from 'react';
const AcceptancePanel = lazy(() => import('./AcceptancePanel'));
export function AcceptanceEvidence({
  runID
}: {
  runID: string;
}) {
  const [open, setOpen] = useState(false);
  return <div className="space-y-2">
    <ActionChip aria-expanded={open} onClick={() => setOpen(value => !value)} icon="shield">验收证据</ActionChip>
    {open && <Suspense fallback={<p role="status">正在加载证据…</p>}><AcceptancePanel key={runID} runID={runID} /></Suspense>}
  </div>;
}
