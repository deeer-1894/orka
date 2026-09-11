import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
const { currentRun, canResumeRun, restoredStatus, lastUserPrompt, RecoveryController } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/runRecovery.js'));
const event = (run = 'new', action = 'failed', extra = {}) => ({ id: `event-${run}-${action}`, type: 'task', action, role: 'system', ts: 20, meta: { conversation_id: 'c', run_id: run, trace_id: `trace-${run}` }, ...extra });
const record = (id = 'new', extra = {}) => ({ run_id: id, conversation_id: 'c', created_at: id === 'new' ? 20 : 10, status: 'failed', resumable: true, trace_id: `trace-${id}`, ...extra });
const context = (extra = {}) => ({ conversationID: 'c', messages: [event()], status: 'error', enabled: true, ...extra });
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };

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
test('only failed/interrupted/partial terminal records may resume', () => {
  for (const status of ['done', 'running', 'paused']) assert.equal(canResumeRun(context(), record('new', { status })), false);
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

test('partial SSE and refreshed partial history are terminal and can offer the current saved run', () => {
  const { terminalStatus, isIncompleteRun } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/runRecovery.js'));
  assert.equal(typeof terminalStatus, 'function');
  assert.equal(terminalStatus(event('new', 'partial')), 'partial');
  assert.equal(restoredStatus([event('new', 'partial')]), 'partial');
  const c = context({ status: 'partial', messages: [event('new', 'partial')] });
  assert.equal(canResumeRun(c, record('new', { status: 'partial' })), true);
  assert.equal(isIncompleteRun(c, record('new', { status: 'partial' })), true);
});
test('legacy done SSE must defer to matching partial run record, but ordinary done stays complete', () => {
  const { isIncompleteRun } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/runRecovery.js'));
  const c = context({ status: 'done', messages: [event('new', 'done')] });
  assert.equal(canResumeRun(c, record('new', { status: 'partial' })), true);
  assert.equal(isIncompleteRun(c, record('new', { status: 'partial' })), true);
  assert.equal(canResumeRun(c, record('new', { status: 'done' })), false);
  assert.equal(isIncompleteRun(c, record('new', { status: 'done' })), false);
});
test('journal retention is not extra budget: exhausted partial runs never offer a resume loop', () => {
  for (const status of ['idle', 'error', 'partial', 'done']) {
    assert.equal(canResumeRun(context({ status }), record('new', { status: 'partial', budget_hit: 'tokens' })), false);
    for (const budget_hit of ['steps', 'time']) assert.equal(canResumeRun(context({ status }), record('new', { status: 'partial', budget_hit })), true);
  }
});
test('late partial resumable settlement enables the button after initial non-resumable record', async () => {
  let saved = false;
  const c = new RecoveryController({ list: async () => [record('new', { status: 'partial', resumable: saved })] });
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
