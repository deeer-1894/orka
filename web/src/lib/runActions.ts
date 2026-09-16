import type { RunRecord } from '../types';
export function resumableRun(run?: RunRecord): boolean {
  return !!run && ['failed', 'partial', 'interrupted'].includes(run.status) && run.resumable === true;
}
export interface RunActionDependencies {
 list: (cid: string) => Promise<RunRecord[]>;
 get: (id: string) => Promise<RunRecord>;
 resume: (id: string, enabledTools?: string[]) => Promise<{resumed:boolean; conversation_id:string}>;
}
export type ResumeToolSelection = (conversationID: string) => string[] | undefined;
// All UI entry points share this owner of submission locks and preflight rules.
export class RunActions {
 private pending = new Set<string>();
 private consumed = new Set<string>();
 constructor(private deps: RunActionDependencies, private changed: () => void = () => {}, private selection?: ResumeToolSelection) {}
 canResume(run: RunRecord) { return resumableRun(run) && !this.consumed.has(run.run_id); }
 isBusy(cid: string) { return this.pending.has(cid); }
 async resume(offered: RunRecord, valid: () => boolean = () => true) {
  const cid = offered.conversation_id;
  if (!this.canResume(offered) || this.isBusy(cid)) return;
  this.pending.add(cid); this.changed();
  try {
   const [runs, exact] = await Promise.all([this.deps.list(cid), this.deps.get(offered.run_id)]);
   if (!valid()) return;
   const own = runs.filter(r => r.conversation_id === cid), newest = Math.max(...own.map(r => r.created_at));
   const latest = own.filter(r => r.created_at === newest);
   if (latest.length !== 1 || latest[0].run_id !== offered.run_id || exact?.run_id !== offered.run_id || exact.conversation_id !== cid || !resumableRun(latest[0]) || !resumableRun(exact)) throw new Error('运行状态已变化，请刷新后重试');
   const response = await this.deps.resume(offered.run_id, this.selection?.(cid));
   if (!response.resumed || response.conversation_id !== cid) throw new Error('服务未确认该任务继续运行');
   this.consumed.add(offered.run_id);
   return response;
  } finally { this.pending.delete(cid); this.changed(); }
 }
}
