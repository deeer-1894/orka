import { useEffect, useRef, useState } from 'react';
import { api } from '../api';
import { RecoveryController, recoveryKey, type RecoveryContext, type ChatStatus } from '../lib/runRecovery';

export function useRunRecovery(context: RecoveryContext, attach: (cid: string) => Promise<ChatStatus | void>, runRevision: number) {
  const [, update] = useState(0);
  const attachRef = useRef(attach);
  attachRef.current = attach;
  const [controller] = useState(() => new RecoveryController({
    list: async cid => (await api.listRuns({ conversation_id: cid })).runs || [],
    get: id => api.getRun(id),
    resume: id => api.resumeRun(id),
    attach: cid => attachRef.current(cid),
  }, () => update(n => n + 1)));
  // Synchronous identity invalidation also covers the interval between a new
  // conversation render and effect cleanup. This does not notify/set React state.
  controller.setContext(context);
  const key = recoveryKey(context);
  useEffect(() => {
    if (!context.conversationID || context.enabled === false || context.status === 'streaming') return;
    const refresh = () => { if (!document.hidden) void controller.refresh(); };
    refresh();
    const timer = window.setInterval(refresh, 3000); // settlement can follow the terminal SSE frame
    window.addEventListener('focus', refresh);
    return () => { window.clearInterval(timer); window.removeEventListener('focus', refresh); };
  }, [controller, key, runRevision]);
  return { ...controller.snapshot(), resume: () => controller.resume(), isBusy: (cid: string) => controller.isBusy(cid) };
}
