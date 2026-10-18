import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
const { currentRun, canResumeRun, restoredStatus, lastUserPrompt, pendingConfirmationID, RecoveryController } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/runRecovery.js'));
const event = (run = 'new', action = 'failed', extra = {}) => ({ id: `event-${run}-${action}`, type: 'task', action, role: 'system', ts: 20, meta: { conversation_id: 'c', run_id: run, trace_id: `trace-${run}` }, ...extra });
const record = (id = 'new', extra = {}) => ({ run_id: id, conversation_id: 'c', created_at: id === 'new' ? 20 : 10, status: 'failed', resumable: true, unfinished: ['remaining work'], trace_id: `trace-${id}`, ...extra });
const context = (extra = {}) => ({ conversationID: 'c', messages: [event()], status: 'error', enabled: true, ...extra });
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };

test('restored confirmations expire on continuation, terminal state or a newer gate', () => {
  const gate = event('old', '', {id:'gate',type:'confirm',payload:{id:'approval',tool:'python',summary:'test'}});
  const paused = event('old','paused');
  assert.equal(pendingConfirmationID([gate, paused], 'c'), 'gate');
  for (const action of ['start','running','done','failed','partial','stopped'])
    assert.equal(pendingConfirmationID([gate,paused,event('new',action)], 'c'), undefined);
  const next = {...gate,id:'next',payload:{...gate.payload,id:'next-approval'}};
  assert.equal(pendingConfirmationID([gate,paused,next], 'c'), 'next');
  assert.equal(pendingConfirmationID([gate,paused,event('old','', {type:'tool'})], 'c'), 'gate');
  assert.equal(pendingConfirmationID([gate,paused,event('old','', {type:'chat',role:'assistant'})], 'c'), undefined);
  assert.equal(pendingConfirmationID([gate,paused,event('new','done',{meta:{conversation_id:'other'}})], 'c'), 'gate');
  assert.equal(pendingConfirmationID([gate,paused,event('old','human_input',{type:'chat',role:'user'})], 'c'), 'gate');
});

test('failure and page reload identify the exact latest run, never old resumable runs', () => {
  assert.equal(currentRun(context(), [record('old'), record()])?.run_id, 'new');
  assert.equal(canResumeRun(context({ status: 'idle' }), record()), true);
  assert.equal(currentRun(context(), [record('old'), record('newer', { created_at: 30 })]), undefined);
  assert.equal(currentRun(context(), [record('old')]), undefined);
  assert.equal(currentRun(context(), [record('new', { conversation_id: 'other' })]), undefined);
});
test('legacy trace identity is exact and an optimistic new turn cannot reuse old identity', () => {
  const legacy = event(); delete legacy.meta.run_id;
  assert.equal(currentRun(context({ messages: [legacy] }), [record()])?.run_id, 'new');
  assert.equal(currentRun(context({ messages: [event(), { ...event(), id: 'local', type: 'chat', role: 'user', meta: { conversation_id: 'c' }, content: 'fresh request' }] }), [record()]), undefined);
  assert.equal(currentRun(context({ messages: [] }), [record()]), undefined);
});
test('only failures, interruptions and errored partial runs may resume', () => {
  for (const status of ['done', 'running', 'paused']) assert.equal(canResumeRun(context(), record('new', { status })), false);
  assert.equal(canResumeRun(context(), record('new', { status: 'partial', error: '' })), false);
  assert.equal(canResumeRun(context(), record('new', { status: 'partial', error: 'provider rate limited' })), true);
  assert.equal(canResumeRun(context(), record('new', { status: 'failed', unfinished: [] })), false);
  assert.equal(canResumeRun(context({ status: 'streaming' }), record()), false);
  assert.equal(canResumeRun(context({ enabled: false }), record()), false);
  assert.equal(canResumeRun(context(), record('new', { resumable: false })), false);
});
test('restored task state and rerun prompt belong to current conversation', () => {
  assert.equal(restoredStatus([event()]), 'error');
  assert.equal(restoredStatus([event(), event('next', 'running')]), 'idle');
  assert.equal(restoredStatus([event('new', 'paused')]), 'paused');
  assert.equal(lastUserPrompt([{ ...event(), type: 'chat', role: 'user', content: 'C request' }, { ...event(), type: 'chat', role: 'user', content: 'other', meta: { conversation_id: 'other' } }], 'c'), 'C request');
});
test('late conversation queries cannot replace the newly selected state', async () => {
  const first = deferred(); let calls = 0;
  const c = new RecoveryController({ list: () => ++calls === 1 ? first.promise : Promise.resolve([record('b', { conversation_id: 'b' })]) });
  c.setContext(context()); const old = c.refresh();
  c.setContext(context({ conversationID: 'b', messages: [event('b', 'failed', { meta: { conversation_id: 'b', run_id: 'b' } })] }));
  await c.refresh(); first.resolve([record()]); await old;
  assert.equal(c.snapshot().run?.run_id, 'b');
});
test('continue rechecks eligibility, uses resume API once, and attaches without a new prompt', async () => {
  const reply = deferred(); const posted = []; const attached = [];
  const c = new RecoveryController({ list: async () => [record()], get: async () => record(), resume: id => { posted.push(id); return reply.promise; }, attach: cid => attached.push(cid) });
  c.setContext(context()); await c.refresh();
  const first = c.resume(); const duplicate = c.resume();
  await new Promise(r => setImmediate(r));
  assert.deepEqual(posted, ['new']);
  reply.resolve({ resumed: true, conversation_id: 'c' }); await first; await duplicate;
  assert.deepEqual(attached, ['c']); await c.refresh();
  assert.equal(c.snapshot().recoverable, false, 'accepted predecessor cannot reappear from stale API response');
});
test('a newer run discovered at click time blocks the stale recovery offer', async () => {
  let latest = [record()]; let posts = 0;
  const c = new RecoveryController({ list: async () => latest, get: async () => record(), resume: async () => { posts++; } });
  c.setContext(context()); await c.refresh(); latest = [record(), record('successor', { created_at: 30, status: 'running' })];
  await c.resume(); assert.equal(posts, 0); assert.equal(c.snapshot().recoverable, false);
});
test('switching conversations during preflight prevents submission and late attach', async () => {
  const single = deferred(); let posts = 0;
  const c = new RecoveryController({ list: async () => [record()], get: () => single.promise, resume: async () => { posts++; } });
  c.setContext(context()); await c.refresh(); const pending = c.resume();
  c.setContext(context({ conversationID: 'other', messages: [] })); single.resolve(record()); await pending;
  assert.equal(posts, 0);
});
test('API rejection stays a failed recovery and never falls back to rerun', async () => {
  const c = new RecoveryController({ list: async () => [record()], get: async () => record(), resume: async () => { throw new Error('expired checkpoint'); } });
  c.setContext(context()); await c.refresh(); await c.resume();
  assert.match(c.snapshot().error, /expired checkpoint/); assert.equal(c.snapshot().busy, false);
});

test('a refresh after an existing live run reattaches once rather than offering a fresh rerun', async () => {
  const attached = [];
  const c = new RecoveryController({ list: async () => [record('new', { status: 'running', resumable: false })], attach: cid => attached.push(cid) });
  c.setContext(context({ status: 'idle' })); await c.refresh(); await c.refresh();
  assert.deepEqual(attached, ['c']); assert.equal(c.snapshot().recoverable, false);
});
test('out-of-order queries for the same failed run do not restore a stale flag', async () => {
  const slow = deferred(); let calls = 0;
  const c = new RecoveryController({ list: () => ++calls === 1 ? slow.promise : Promise.resolve([record('new', { resumable: false })]) });
  c.setContext(context()); const old = c.refresh(); await c.refresh(); slow.resolve([record()]); await old;
  assert.equal(c.snapshot().recoverable, false);
});
test('wrong conversation response cannot attach, and claim locks clear on rejection', async () => {
  const attached = [];
  const c = new RecoveryController({ list: async () => [record()], get: async () => record(), resume: async () => ({ resumed: true, conversation_id: 'other' }), attach: cid => attached.push(cid) });
  c.setContext(context()); await c.refresh(); await c.resume();
  assert.deepEqual(attached, []); assert.equal(c.isBusy('c'), false); assert.notEqual(c.snapshot().error, '');
});
test('new input while a query is loading invalidates the prior failed offer', async () => {
  const q = deferred(); const c = new RecoveryController({ list: () => q.promise });
  c.setContext(context()); const loading = c.refresh();
  c.setContext(context({ status: 'streaming', messages: [event(), { ...event(), type: 'chat', role: 'user', meta: { conversation_id: 'c' }, id: 'fresh' }] }));
  q.resolve([record()]); await loading; assert.equal(c.snapshot().recoverable, false); assert.equal(c.snapshot().run, undefined);
});

test('an accepted continuation attaches to its original conversation after switching away', async () => {
  const accepted = deferred(); const attached = [];
  const c = new RecoveryController({ list: async () => [record()], get: async () => record(), resume: () => accepted.promise, attach: cid => attached.push(cid) });
  c.setContext(context()); await c.refresh(); const resuming = c.resume();
  await new Promise(r => setImmediate(r));
  c.setContext(context({ conversationID: 'other', messages: [] }));
  accepted.resolve({ resumed: true, conversation_id: 'c' }); await resuming;
  assert.deepEqual(attached, ['c']); assert.equal(c.snapshot().run, undefined);
});

test('the stream and refreshed history share terminal handling including confirmation pauses', () => {
  const { terminalStatus } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/runRecovery.js'));
  assert.equal(typeof terminalStatus, 'function');
  assert.equal(terminalStatus(event('new', 'paused')), 'paused');
  assert.equal(terminalStatus(event('new', '', { type: 'confirm' })), 'paused');
  assert.equal(terminalStatus(event('new', '', { type: 'clarify' })), 'paused');
  assert.equal(terminalStatus(event()), 'error');
  assert.equal(terminalStatus(event('new', 'done')), 'done');
  assert.equal(terminalStatus(event('new', 'running')), undefined);
});

test('partial SSE is terminal but plan-only partial history does not offer another run', () => {
  const { terminalStatus, isIncompleteRun } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/runRecovery.js'));
  assert.equal(typeof terminalStatus, 'function');
  assert.equal(terminalStatus(event('new', 'partial')), 'partial');
  assert.equal(restoredStatus([event('new', 'partial')]), 'partial');
  const c = context({ status: 'partial', messages: [event('new', 'partial')] });
  assert.equal(canResumeRun(c, record('new', { status: 'partial' })), false);
  assert.equal(isIncompleteRun(c, record('new', { status: 'partial' })), true);
});
test('legacy done SSE recognizes matching partial state without offering plan-only continuation', () => {
  const { isIncompleteRun } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/runRecovery.js'));
  const c = context({ status: 'done', messages: [event('new', 'done')] });
  assert.equal(canResumeRun(c, record('new', { status: 'partial' })), false);
  assert.equal(isIncompleteRun(c, record('new', { status: 'partial' })), true);
  assert.equal(canResumeRun(c, record('new', { status: 'done' })), false);
  assert.equal(isIncompleteRun(c, record('new', { status: 'done' })), false);
});
test('retired budget markers never create a continuation offer', () => {
  for (const status of ['idle', 'error', 'partial', 'done']) {
    assert.equal(canResumeRun(context({ status }), record('new', { status: 'partial', budget_hit: 'tokens' })), false);
    for (const budget_hit of ['steps', 'time']) assert.equal(canResumeRun(context({ status }), record('new', { status: 'partial', budget_hit })), false);
  }
});
test('late partial resumable settlement enables the button after initial non-resumable record', async () => {
  let saved = false;
  const c = new RecoveryController({ list: async () => [record('new', { status: 'interrupted', resumable: saved })] });
  c.setContext(context({ status: 'partial', messages: [event('new', 'partial')] }));
  await c.refresh(); assert.equal(c.snapshot().recoverable, false);
  saved = true; await c.refresh(); assert.equal(c.snapshot().recoverable, true);
});

test('failed live attachment can retry after backoff, with one connection in flight and a finite limit', async () => {
  let now = 0; const replies = []; let calls = 0;
  const c = new RecoveryController({ now: () => now, list: async () => [record('new', { status: 'running' })], attach: () => { calls++; const d = deferred(); replies.push(d); return d.promise; } });
  c.setContext(context({ status: 'idle' })); await c.refresh(); await c.refresh();
  assert.equal(calls, 1);
  replies[0].resolve('error'); await new Promise(r => setImmediate(r));
  c.setContext(context({ status: 'error' })); await c.refresh(); assert.equal(calls, 1);
  now = 3000; await c.refresh(); assert.equal(calls, 2);
  await c.refresh(); assert.equal(calls, 2);
  replies[1].resolve('error'); await new Promise(r => setImmediate(r));
  now = 9000; await c.refresh(); assert.equal(calls, 3);
  replies[2].resolve('error'); await new Promise(r => setImmediate(r));
  now = 999999; await c.refresh(); assert.equal(calls, 3);
});

test('rejected attachment is retryable and never creates an unhandled rejection', async () => {
  let now = 0, calls = 0;
  const c = new RecoveryController({ now: () => now, list: async () => [record('new', { status: 'running' })], attach: async () => { calls++; throw new Error('connection unavailable'); } });
  c.setContext(context()); await c.refresh(); await new Promise(r => setImmediate(r));
  now = 3000; await c.refresh(); assert.equal(calls, 2);
});

const userMessage = (id, ts, content = 'repeat') => ({ ...event(), id, ts, type: 'chat', role: 'user', content });
test('late history merges by ID during streaming, preserves live values/state and replaces only the matching optimistic echo', () => {
  const { hydrateConversation } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/runRecovery.js'));
  const old = userMessage('old', 1), local = userMessage('local-100', 100), saved = userMessage('saved', 101);
  const live = event('new', 'running', { ts: 102, content: 'live' });
  const got = hydrateConversation({ status: 'streaming', messages: [local, live] }, [old, saved, { ...live, content: 'stale' }], 'c', true);
  assert.equal(got.status, 'streaming'); assert.deepEqual(got.messages.map(m => m.id), ['old', 'saved', live.id]);
  assert.equal(got.messages.at(-1).content, 'live');
});
test('late history after completion restores old turns without overwriting the just-completed status', () => {
  const { hydrateConversation } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/runRecovery.js'));
  const done = event('new', 'partial', { ts: 200 });
  const got = hydrateConversation({ status: 'partial', messages: [userMessage('local-100', 100), done] }, [userMessage('old', 1), userMessage('saved', 101), event('old', 'done', { ts: 2 })], 'c', true);
  assert.equal(got.status, 'partial'); assert.deepEqual(got.messages.map(m => m.id), ['old', 'event-old-done', 'saved', done.id]);
});
test('history hydration does not collapse repeated human requests or drop an unacknowledged echo', () => {
  const { hydrateConversation } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/runRecovery.js'));
  const got = hydrateConversation({ status: 'error', messages: [userMessage('local-100', 100), userMessage('local-200', 200)] }, [userMessage('old', 1), userMessage('saved', 101)], 'c', true);
  assert.deepEqual(got.messages.map(m => m.id), ['old', 'saved', 'local-200']);
  const restored = hydrateConversation({ status: 'idle', messages: [] }, [event()], 'c', false);
  assert.equal(restored.status, 'error');
});


test('confirmed empty history attaches to latest running once without resume', async () => {
  const attached = [], resumed = [];
  const c = new RecoveryController({ list: async () => [record('new', { status: 'running' })], attach: cid => attached.push(cid), resume: async id => resumed.push(id) });
  c.setContext(context({ messages: [], status: 'idle', historyLoaded: true }));
  await c.refresh(); await c.refresh(); await c.resume();
  assert.deepEqual(attached, ['c']); assert.deepEqual(resumed, []);
  assert.equal(c.snapshot().recoverable, false);
});
test('blank fallback requires loaded history and protects identities, statuses and ambiguous latest', () => {
  const blank = context({ messages: [], status: 'idle', historyLoaded: true });
  const running = record('new', { status: 'running' });
  assert.equal(currentRun(blank, [running])?.run_id, 'new');
  for (const c of [
    { ...blank, historyLoaded: false }, { ...blank, historyLoaded: undefined },
    { ...blank, status: 'streaming' }, { ...blank, status: 'paused' },
    { ...blank, enabled: false }, { ...blank, conversationID: '' },
    { ...blank, messages: [{ ...userMessage('saved-user', 1), meta: { conversation_id: 'c' } }] },
    { ...blank, messages: [{ ...userMessage('local-1', 1), meta: { conversation_id: 'c' } }] },
  ]) assert.equal(currentRun(c, [running]), undefined);
  assert.equal(currentRun(blank, [{ ...running, conversation_id: 'other' }]), undefined);
  assert.equal(currentRun(blank, [running, { ...running, run_id: 'ambiguous' }]), undefined);
  assert.equal(currentRun(blank, [running, record('later', { created_at: 30, status: 'done' })]), undefined);
  for (const status of ['done', 'failed', 'partial', 'interrupted', 'paused']) assert.equal(currentRun(blank, [{ ...running, status }]), undefined);
});
test('empty-history query is invalidated by a local send or conversation switch', async () => {
  for (const next of [context({ conversationID: 'other', messages: [], status: 'idle', historyLoaded: true }), context({ status: 'streaming', messages: [userMessage('local-1', 1)], historyLoaded: true })]) {
    const q = deferred(), attached = [];
    const c = new RecoveryController({ list: () => q.promise, attach: cid => attached.push(cid) });
    c.setContext(context({ messages: [], status: 'idle', historyLoaded: true }));
    const pending = c.refresh(); c.setContext(next);
    q.resolve([record('new', { status: 'running' })]); await pending;
    assert.deepEqual(attached, []);
  }
});
test('late empty hydration cannot clear attached messages or terminal state', () => {
  const { hydrateConversation } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/runRecovery.js'));
  const messages = [userMessage('server-user', 1), event('new', 'done')];
  assert.deepEqual(hydrateConversation({ messages, status: 'done' }, [], 'c', true), { messages, status: 'done' });
});
