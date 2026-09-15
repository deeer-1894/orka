import { useSyncExternalStore } from 'react';
const revisions = new Map<string, number>();
const listeners = new Set<() => void>();
let globalRevision = 0;
export function invalidateSessionFiles(cid?: string) {
 if (cid) revisions.set(cid, (revisions.get(cid) || 0) + 1); else globalRevision++;
 listeners.forEach(fn => fn());
}
export function useFileRevision(cid: string) {
 return useSyncExternalStore(fn => { listeners.add(fn); return () => listeners.delete(fn); }, () => `${globalRevision}:${revisions.get(cid) || 0}`);
}
