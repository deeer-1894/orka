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

const meta = { conversation_id: 'busy', run_id: 'run-1', task_id: '', trace_id: 'trace-1' };
const messages = [
  { id: 'ask', type: 'chat', role: 'user', ts: 1000, content: '整理本周的数据', meta },
  { id: 'answer', type: 'chat', role: 'assistant', ts: 2000, content: '模型服务暂时繁忙，本轮已中断。', meta },
  { id: 'failed', type: 'task', action: 'failed', role: 'system', ts: 3000, meta },
];
const run = (extra = {}) => ({ run_id: 'run-1', conversation_id: 'busy', status: 'failed', resumable: true, unfinished: ['模型服务繁忙或暂时不可用，任务尚未完成'],
  error: '[NodeRunError] exceeds max retries: last error: llm status 429', trace_id: 'trace-1', created_at: 1000, ...extra });

async function fixture(t, recovery) {
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
  await page.evaluate(state => window.fileTest.set(state), { mode: 'thread', conversationID: 'busy', messages, status: 'error', recovery });
  t.after(() => assert.deepEqual(errors, []));
  return page;
}

test('a run waiting out a provider outage says when it will continue and can be cancelled', async t => {
  const page = await fixture(t, { run: run({ auto_resume_at: Date.now() + 4.5 * 60_000 }), recoverable: true, busy: false, checking: false, error: '' });
  const line = page.getByTestId('auto-resume');
  await line.waitFor();
  assert.equal(await line.textContent(), '服务繁忙，将在 5 分钟后自动继续');
  await page.getByRole('button', { name: '立即继续' }).waitFor();
  await page.getByRole('button', { name: '取消自动继续' }).click();
  await page.getByTestId('cancelled').filter({ hasText: '1' }).waitFor();
  assert.equal(await line.count(), 0);
  // The manual offer survives the cancellation.
  await page.getByRole('button', { name: '继续未完成任务' }).waitFor();
  assert.equal(await page.getByRole('button', { name: '取消自动继续' }).count(), 0);
});

test('an ordinary failure shows no automatic-resume promise', async t => {
  const page = await fixture(t, { run: run(), recoverable: true, busy: false, checking: false, error: '' });
  await page.getByRole('button', { name: '继续未完成任务' }).waitFor();
  assert.equal(await page.getByTestId('auto-resume').count(), 0);
  assert.equal(await page.getByRole('button', { name: '取消自动继续' }).count(), 0);
});
