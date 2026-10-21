import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const { runProgress, stepGroups, stepSummary, deliverySummary, elapsedLabel } = require(`${process.env.ORKA_TEST_BUILD}/lib/runProgress.js`);

let n = 0;
const meta = { conversation_id: 'c', task_id: '', trace_id: '', run_id: 'r1' };
const msg = (type, extra = {}) => ({ id: 'm' + (++n), type, role: 'assistant', meta, ts: 1000 * n, ...extra });
const plan = (...statuses) => msg('plan', { payload: { steps: statuses.map((status, i) => ({ title: 'S' + (i + 1), status })), outputs: ['report.md'] } });
const tool = (name, agent) => msg('tool', { payload: { tool: name }, meta: agent ? { ...meta, agent_id: agent } : meta });

test('tool calls are filed under the plan step in focus when they ran', () => {
  const before = tool('current_time');
  const p1 = plan('active', 'pending', 'pending');
  const a = tool('web_search'), b = tool('web_search'), c = tool('fetch_url');
  const p2 = plan('done', 'active', 'pending');
  const d = tool('python', 'engineer');
  const all = [msg('task', { action: 'start' }), before, p1, a, b, c, p2, d];
  const groups = stepGroups(all, [before, a, b, c, d]);
  assert.deepEqual(groups.map(g => [g.title, g.index, g.items.length]), [['准备', -1, 1], ['S1', 0, 3], ['S2', 1, 1]]);
  assert.equal(stepSummary(groups[1].items), '搜索 2 次 · 读取网页 1 个');

  const progress = runProgress(all);
  assert.equal(progress.total, 3);
  assert.equal(progress.done, 1);
  assert.deepEqual(progress.current, { index: 1, title: 'S2' });
  assert.equal(progress.toolCalls, 5);
  assert.equal(progress.startedAt, all[0].ts);
  assert.deepEqual(progress.agents, ['engineer']);
});

test('a blocked step does not hold the focus', () => {
  const all = [plan('blocked', 'pending')];
  assert.deepEqual(runProgress(all).current, { index: 1, title: 'S2' });
});

test('delivery summary appears only once the run ended, listing what is left', () => {
  const p = msg('plan', { payload: { steps: [{ title: 'Write', status: 'done' }, { title: 'Test', status: 'blocked', reason: 'no python' }], outputs: ['report.md'] } });
  const running = [msg('task', { action: 'start' }), p];
  assert.equal(deliverySummary(running), undefined);
  const ended = [...running, msg('task', { action: 'partial' })];
  assert.deepEqual(deliverySummary(ended), { status: 'partial', done: 1, total: 2, unfinished: [{ title: 'Test', reason: 'no python' }], outputs: ['report.md'] });
  assert.equal(deliverySummary([msg('task', { action: 'done' })]), undefined, 'no plan, no card');
});

test('elapsed label', () => {
  assert.equal(elapsedLabel(42_000), '42 秒');
  assert.equal(elapsedLabel(372_000), '6 分 12 秒');
  assert.equal(elapsedLabel(3_780_000), '1 小时 3 分');
});
