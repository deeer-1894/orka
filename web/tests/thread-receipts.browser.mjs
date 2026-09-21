import { before, after, test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'vite';
import { chromium } from 'playwright';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('../', import.meta.url));
let server, browser, origin;
before(async () => {
  server = await createServer({ root, server: { port: 0, host: '127.0.0.1' } });
  await server.listen();
  origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] });
});
after(async () => { await browser?.close(); await server?.close(); });
const message = (id, type, extra = {}) => ({ id, type, role: 'assistant', ts: 1000,
  meta: { conversation_id: 'receipts', run_id: 'run-1', task_id: '', trace_id: 'trace-1' }, ...extra });
const tool = (id, ts, receipt) => message(id, 'tool', { ts, payload: { tool: 'browser', error: '',
  args: { action: receipt.action }, result: JSON.stringify(receipt) + '\n[Plan browser evidence] ' +
    JSON.stringify({ step: 'inspect', receipt: { id, ok: receipt.ok } }) + '\nAfter recovery, cite the successful receipt id in evidence_ids.' } });
async function fixture(t, messages, status = 'streaming') {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' });
  t.after(() => context.close());
  const errors = [];
  await context.route('**/*', route => {
    const url = new URL(route.request().url());
    if (url.origin !== origin) return route.fulfill({ body: '', contentType: 'text/css' });
    if (!url.pathname.startsWith('/api/')) return route.continue();
    return route.fulfill({ json: { code: 0, data: url.pathname.endsWith('/delivery/list') ? { deliveries: [] } : [] } });
  });
  const page = await context.newPage();
  page.setDefaultTimeout(4000);
  page.on('pageerror', e => errors.push(e.message));
  await page.goto(origin + '/tests/session-files.html');
  await page.waitForFunction(() => !!window.fileTest);
  await page.evaluate(({ messages, status }) => window.fileTest.set({ mode: 'thread', conversationID: 'receipts', messages, status }), { messages, status });
  t.after(() => assert.deepEqual(errors, []));
  return page;
}

test('real browser receipt shapes drive failure nodes, action labels and measured batch time', async t => {
  const messages = [
    tool('snapshot-1', 10_000, { ok: true, action: 'snapshot', elapsed_ms: 600, url: 'https://example.com/one' }),
    tool('snapshot-2', 70_000, { ok: true, action: 'snapshot', elapsed_ms: 800, url: 'https://example.com/two' }),
    tool('failure-1', 130_000, { ok: false, action: 'evaluate', elapsed_ms: 900, error: { code: 'script_error', message: 'Browser page script failed.' } }),
    tool('failure-2', 190_000, { ok: false, action: 'evaluate', elapsed_ms: 500 }),
    tool('open', 240_000, { ok: true, action: 'open', elapsed_ms: 750, url: 'https://example.com/final' }),
  ];
  const page = await fixture(t, messages);
  const batch = page.getByRole('button', { name: /读取页面 · 2 次/ });
  await batch.waitFor();
  assert.match(await batch.textContent(), /1\.4s/);
  assert.equal(await page.getByTitle('失败', { exact: true }).count(), 2);
  await page.getByText(/Browser page script failed/).waitFor();
  await page.getByText('打开网页', { exact: true }).waitFor();
  assert.equal(await page.getByText(/委派 browser/).count(), 0);
  assert.equal(await page.getByText(/Plan browser evidence/).count(), 0);
  assert.equal(await page.getByText(/60\.0s|50\.0s/).count(), 0);
  await batch.click();
  await page.getByText('0.6s', { exact: true }).waitFor();
  await page.getByText('· https://example.com/one', { exact: true }).waitFor();
  await page.getByText('收起这 2 步', { exact: true }).waitFor();
  await page.getByRole('button', { name: '收起这 2 步', exact: true }).click();
  await batch.waitFor();
});

test('legacy calls and partly measured batches never use gaps between messages as elapsed time', async t => {
  const page = await fixture(t, [
    message('legacy-1', 'tool', { ts: 1000, payload: { tool: 'shell', result: 'legacy output' } }),
    message('legacy-2', 'tool', { ts: 91_000, payload: { tool: 'shell', result: 'legacy output' } }),
    tool('measured', 191_000, { ok: true, action: 'snapshot', elapsed_ms: 600 }),
    tool('unmeasured', 291_000, { ok: true, action: 'snapshot' }),
  ]);
  const batch = page.getByRole('button', { name: /读取页面 · 2 次/ });
  await batch.waitFor();
  assert.doesNotMatch(await batch.textContent(), /\ds\b/);
  await page.getByRole('button', { name: /shell · 2 次/ }).click();
  assert.equal(await page.getByText(/^\d+\.\d+s$/).count(), 0);
});

test('blocked plans show reasons and remain incomplete, including persisted history', async t => {
  const plan = message('plan-1', 'plan', { payload: { steps: [
    { title: '已读取', status: 'done' },
    { title: '正在整理', status: 'active' },
    { title: '等待复核', status: 'pending' },
    { title: '发布受限', status: 'blocked', reason: '缺少访问权限', evidence_ids: ['internal-receipt-42'] },
  ] } });
  const page = await fixture(t, [plan], 'idle');
  await page.getByText('· 1/4', { exact: true }).waitFor();
  const blocked = page.getByRole('listitem').filter({ hasText: '发布受限' });
  assert.match(await blocked.textContent(), /受阻：缺少访问权限/);
  assert.doesNotMatch(await blocked.textContent(), /✓/);
  assert.equal(await blocked.locator('.line-through, .bg-ok').count(), 0);
  assert.equal(await page.getByText('已完成', { exact: true }).count(), 0);
  assert.equal(await page.getByText(/internal-receipt-42/).count(), 0);
  await page.evaluate(plan => window.fileTest.set({ status: 'streaming', messages: [plan] }),
    { ...plan, payload: { steps: [{ title: '发布受限', status: 'blocked' }] } });
  await page.getByText('· 0/1', { exact: true }).waitFor();
  await page.getByText('· 受阻', { exact: true }).waitFor();
  assert.equal(await page.getByText('进行中', { exact: true }).count(), 0);
});

test('persisted steering is acknowledged beside the user message without claiming adoption', async t => {
  const page = await fixture(t, [
    message('initial', 'chat', { role: 'user', action: 'human_input', content: '初始要求' }),
    message('steer', 'chat', { role: 'user', action: 'human_input', content: '追加要求', payload: { request_id: 'request-1', file_ids: [] } }),
  ], 'idle');
  await page.getByText('已加入当前任务', { exact: true }).waitFor();
  assert.equal(await page.getByText('已加入当前任务', { exact: true }).count(), 1);
  const bubble = page.getByText('追加要求', { exact: true }).locator('..');
  assert.match(await bubble.textContent(), /已加入当前任务/);
  assert.equal(await page.getByText(/已采纳/).count(), 0);
});
