import type { Message, RunRecord } from '../types';

export type ChatStatus = 'idle' | 'streaming' | 'paused' | 'error' | 'done' | 'partial';
export interface RecoveryContext { conversationID: string; messages: Message[]; status: ChatStatus; enabled?: boolean }
export interface RecoverySnapshot { key: string; run?: RunRecord; recoverable: boolean; checking: boolean; busy: boolean; error: string }
export interface RecoveryDependencies {
  list: (cid: string) => Promise<RunRecord[]>;
  get?: (runID: string) => Promise<RunRecord>;
  resume?: (runID: string) => Promise<{ resumed: boolean; conversation_id: string }>;
  attach?: (cid: string) => void | Promise<ChatStatus | void>;
  now?: () => number;
}

function identity(c: RecoveryContext) {
  for (let i = c.messages.length - 1; i >= 0; i--) {
    const m = c.messages[i];
    if (m.meta?.conversation_id !== c.conversationID || m.type === 'heartbeat') continue;
    if (m.meta.run_id || m.meta.trace_id) return { run: m.meta.run_id, trace: m.meta.trace_id };
    // A just-sent user message belongs to a new run, not the previous failure.
    if (m.type === 'chat' && m.role === 'user') return undefined;
  }
  return undefined;
}

export function recoveryKey(c: RecoveryContext): string {
  const id = identity(c);
  const user = [...c.messages].reverse().find(m => m.meta?.conversation_id === c.conversationID && m.type === 'chat' && m.role === 'user');
  return JSON.stringify([c.conversationID, c.enabled !== false, c.status, id?.run, id?.trace, user?.id]);
}

export function currentRun(c: RecoveryContext, runs: RunRecord[]): RunRecord | undefined {
  const id = identity(c);
  if (!id || !c.conversationID || c.enabled === false) return undefined;
  const own = runs.filter(r => r.conversation_id === c.conversationID);
  const newest = Math.max(...own.map(r => r.created_at));
  const latest = own.filter(r => r.created_at === newest);
  if (latest.length !== 1) return undefined; // ambiguous ordering fails closed
  const r = latest[0];
  return (id.run ? r.run_id === id.run : r.trace_id === id.trace) ? r : undefined;
}

export function isIncompleteRun(c: RecoveryContext, run?: RunRecord): boolean {
  if (c.status === 'streaming' || c.status === 'paused') return false;
  if (run) return run.conversation_id === c.conversationID && ['failed', 'partial', 'interrupted'].includes(run.status);
  return c.status === 'error' || c.status === 'partial';
}

export function canResumeRun(c: RecoveryContext, run?: RunRecord): boolean {
  // resumable says a journal survived, not that its retained allowance remains.
  // Token allowance survives resume; per-attempt limits are rebuilt by the
  // backend. Do not infer a new token allowance from done/partial presentation.
  return c.enabled !== false && isIncompleteRun(c, run) &&
    run?.conversation_id === c.conversationID && run.resumable === true && run.budget_hit !== 'tokens';
}

export function terminalStatus(m: Message): ChatStatus | undefined {
  if (m.type === 'confirm' || m.type === 'clarify') return 'paused';
  if (m.type !== 'task') return undefined;
  if (m.action === 'failed') return 'error';
  if (m.action === 'partial') return 'partial';
  if (m.action === 'done') return 'done';
  if (m.action === 'paused') return 'paused';
  return undefined;
}

export function restoredStatus(messages: Message[]): ChatStatus {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    const terminal = terminalStatus(m);
    if (terminal) return terminal;
    if (m.type === 'task' && (m.action === 'running' || m.action === 'start')) return 'idle';
    if (m.type === 'chat' && m.role === 'user') return 'idle';
  }
  return 'idle';
}

export function lastUserPrompt(messages: Message[], cid: string): string {
  return [...messages].reverse().find(m => m.meta?.conversation_id === cid && m.type === 'chat' && m.role === 'user')?.content || '';
}

// Async controller without React: every response is bound to the context that
// requested it. Pending/consumed IDs protect rapid clicks and stale server reads.
export class RecoveryController {
  private context: RecoveryContext = { conversationID: '', messages: [], status: 'idle' };
  private state: RecoverySnapshot = { key: '', recoverable: false, checking: false, busy: false, error: '' };
  private revision = 0;
  private query = 0;
  private pending = new Set<string>();
  private pendingConversations = new Set<string>();
  private consumed = new Set<string>();
  private attachments = new Map<string, { attempts: number; inFlight: boolean; retryAt: number }>();
  constructor(private deps: RecoveryDependencies, private changed: () => void = () => {}) {}
  setContext(context: RecoveryContext) {
    const key = recoveryKey(context);
    this.context = context;
    if (key === this.state.key) return;
    this.revision++;
    this.query++;
    this.state = { key, recoverable: false, checking: false, busy: this.isBusy(context.conversationID), error: '' };
  }
  isBusy(cid: string): boolean { return this.pendingConversations.has(cid); }
  snapshot(): RecoverySnapshot { return this.state; }
  private publish(update: Partial<RecoverySnapshot>) {
    this.state = { ...this.state, ...update };
    this.state.busy = this.pendingConversations.has(this.context.conversationID);
    this.state.recoverable = canResumeRun(this.context, this.state.run) && !this.consumed.has(this.state.run!.run_id);
    this.changed();
  }
  private attachRunning(run: RunRecord) {
    if (!this.deps.attach) return;
    const now = () => this.deps.now?.() ?? Date.now();
    const attempt = this.attachments.get(run.run_id) || { attempts: 0, inFlight: false, retryAt: 0 };
    if (attempt.inFlight || attempt.attempts >= 3 || now() < attempt.retryAt) return;
    attempt.inFlight = true;
    attempt.attempts++;
    this.attachments.set(run.run_id, attempt);
    const settled = (failed: boolean) => {
      attempt.inFlight = false;
      attempt.retryAt = failed ? now() + 3000 * attempt.attempts : Infinity;
    };
    try {
      // The promise spans the SSE attachment, so polling cannot open a second
      // connection. A transport failure releases it for a bounded later retry.
      Promise.resolve(this.deps.attach(run.conversation_id)).then(
        status => settled(status === 'error'), () => settled(true),
      );
    } catch { settled(true); }
  }
  async refresh() {
    const c = this.context, revision = this.revision, query = ++this.query;
    if (!c.conversationID || c.enabled === false || c.status === 'streaming' || this.isBusy(c.conversationID)) return;
    this.publish({ checking: true });
    try {
      const runs = await this.deps.list(c.conversationID);
      if (revision !== this.revision || query !== this.query) return;
      const run = currentRun(c, runs);
      this.publish({ run });
      if (run?.status === 'running' && (c.status === 'idle' || c.status === 'error') ) {
        this.attachRunning(run);
      }
    } catch {
      if (revision === this.revision && query === this.query) this.publish({ run: undefined });
    } finally {
      if (revision === this.revision && query === this.query) this.publish({ checking: false });
    }
  }
  async resume() {
    const c = this.context, revision = this.revision, offered = this.state.run;
    if (!canResumeRun(c, offered) || !offered || this.pending.has(offered.run_id) || this.consumed.has(offered.run_id)) return;
    this.pending.add(offered.run_id);
    this.pendingConversations.add(c.conversationID);
    this.query++; // a pre-click query must not restore this offer
    this.publish({ busy: true, error: '' });
    try {
      const [runs, exact] = await Promise.all([this.deps.list(c.conversationID), this.deps.get!(offered.run_id)]);
      if (revision !== this.revision) return;
      const latest = currentRun(c, runs);
      if (latest?.run_id !== offered.run_id || exact.run_id !== offered.run_id || !canResumeRun(c, latest) || !canResumeRun(c, exact)) {
        this.publish({ run: undefined });
        throw new Error('运行状态已变化，请刷新后重试');
      }
      const response = await this.deps.resume!(offered.run_id);
      if (!response.resumed || response.conversation_id !== c.conversationID) throw new Error('服务未确认该任务继续运行');
      this.consumed.add(offered.run_id);
      if (revision === this.revision) this.publish({ run: undefined });
      // Attaching is conversation-scoped and does not navigate. Switching away
      // must not orphan an accepted background continuation; a newer turn in
      // this same conversation still invalidates the attachment.
      if (this.context.conversationID !== c.conversationID || recoveryKey(this.context) === recoveryKey(c)) {
        void Promise.resolve(this.deps.attach?.(c.conversationID)).catch(() => {});
      }
    } catch (error) {
      if (revision === this.revision) this.publish({ error: error instanceof Error ? error.message : '无法继续该任务' });
    } finally {
      this.pending.delete(offered.run_id);
      this.pendingConversations.delete(c.conversationID);
      if (revision === this.revision) this.publish({ busy: false });
    }
  }
}


/** Merge late persisted history with the current stream, retaining live IDs and
 * status. Local echoes are paired one-to-one only with a later server user echo
 * in the same submission window; earlier repeated requests remain distinct. */
export function hydrateConversation(
  current: { messages: Message[]; status: ChatStatus }, history: Message[], cid: string, preserveStatus: boolean,
): { messages: Message[]; status: ChatStatus } {
  const byID = new Map<string, Message>();
  for (const m of [...history, ...current.messages]) {
    if (m.meta?.conversation_id === cid) byID.set(m.id, m);
  }
  const messages = [...byID.values()].sort((a, b) => a.ts - b.ts);
  const locals = messages.filter(m => m.type === 'chat' && m.role === 'user' && m.id.startsWith('local-'));
  const used = new Set<string>(), replaced = new Set<string>();
  for (let i = 0; i < locals.length; i++) {
    const local = locals[i], until = locals[i + 1]?.ts ?? Infinity;
    const echo = messages.find(m => m.type === 'chat' && m.role === 'user' && !m.id.startsWith('local-') &&
      !used.has(m.id) && m.content === local.content && m.ts >= local.ts && m.ts < until);
    if (echo) { used.add(echo.id); replaced.add(local.id); }
  }
  const merged = messages.filter(m => !replaced.has(m.id));
  return { messages: merged, status: preserveStatus ? current.status : restoredStatus(merged) };
}
