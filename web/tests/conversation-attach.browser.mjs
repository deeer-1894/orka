// Isolated headless context and mocked APIs; no real accounts or model requests.
import { before, after, test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'vite';
import { chromium } from 'playwright';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('../', import.meta.url)), BASE = '/api/v1/controller';
let server, browser, origin;
before(async () => {
  server = await createServer({ root, configFile: root + 'vite.config.ts', server: { port: 0, host: '127.0.0.1' } });
  await server.listen(); origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] });
});
after(async () => { await browser?.close(); await server?.close(); });
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
const message = (content, id = 'answer', extra = {}) => ({ id, type: 'chat', role: 'assistant', content, created_at: 10, meta: { conversation_id: 'c', trace_id: 'trace-current' }, ...extra });
const running = { run_id: 'run-current', conversation_id: 'c', trace_id: 'trace-current', status: 'running', created_at: 10, tokens: 0, tool_calls: 0 };
async function app(t, { history = async () => [], runs = [] } = {}) {
  const context = await browser.newContext({ viewport: { width: 1400, height: 1000 }, serviceWorkers: 'block' });
  t.after(() => context.close());
  await context.addInitScript(() => localStorage.setItem('orka.token', 'isolated-attach-test'));
  const requests = [], errors = [], unexpected = [];
  let historyCalls = 0, attachCalls = 0;
  await context.route('**/*', async route => {
    const req = route.request(), url = new URL(req.url());
    if (url.origin === 'https://fonts.googleapis.com') return route.fulfill({ contentType: 'text/css', body: '' });
    if (url.origin !== origin) { unexpected.push(url.origin); return route.abort(); }
    if (!url.pathname.startsWith('/api/')) return route.continue();
    const path = url.pathname.slice(BASE.length), body = req.postDataJSON() || {};
    requests.push({ path, body });
    const json = data => route.fulfill({ json: { code: 0, data } });
    switch (path) {
      case '/auth/me': return json({ email: 'attach-test@example.invalid', name: 'Attach test' });
      case '/models': return json([{ version: 'auto', label: 'Auto', hint: 'Default' }]);
      case '/conversation/list': return json([{ conversation_id: 'c', title: 'Running snapshot', owner_email: 'attach-test@example.invalid', created_at: 10 }]);
      case '/conversation/shared-with-me': case '/file/list': return json([]);
      case '/conversation/get-messages': return json(await history(++historyCalls));
      case '/run/list': return json({ runs });
      case '/task/get-tasks': return json({ tasks: [], owners: {} });
      case '/skill/list': return json({ skills: [] });
      case '/notification/list': return json({ notifications: [], unread: 0 });
      case '/metrics': return json({ total_tokens: 0 });
      case '/artifact/list': return json({ artifacts: [] });
      case '/artifact/by-conversation': return route.fulfill({ status: 404, json: { code: 404, msg: 'No artifact' } });
      case '/events': return route.fulfill({ status: 503, body: '' });
      case '/chat/followups': return json({ suggestions: [] });
      case '/chat/attach': {
        attachCalls++;
        assert.equal(url.searchParams.get('conversation_id'), 'c');
        const frames = [message('Attached live answer'), { id: 'finished', type: 'task', action: 'done', ts: 50, meta: { conversation_id: 'c', trace_id: 'trace-current' } }];
        return route.fulfill({ contentType: 'text/event-stream', body: frames.map((m, i) => `id: ${i + 1}
data: ${JSON.stringify({ ...m, ts: m.ts || m.created_at || 10 })}

`).join('') });
      }
      default: unexpected.push(path); return route.fulfill({ status: 500, json: { code: 500, msg: 'unexpected mock request' } });
    }
  });
  const page = await context.newPage(); page.setDefaultTimeout(4000);
  page.on('pageerror', e => errors.push(e.message));
  await page.goto(origin); await page.getByText('Running snapshot', { exact: true }).waitFor();
  return { page, counts: () => ({ historyCalls, attachCalls }),
    select: () => page.getByText('Running snapshot', { exact: true }).first().click(),
    check: () => { assert.deepEqual(errors, []); assert.deepEqual(unexpected, []); assert.equal(requests.filter(r => ['/chat/run', '/chat/resume_run', '/run/rerun'].includes(r.path)).length, 0); },
  };
}

test('previously empty history is fetched again on selection', async t => {
  const a = await app(t, { history: async count => count === 1 ? [] : [message('Later persisted answer')] });
  await a.select(); await a.page.getByText('交给 Orka 去执行', { exact: true }).waitFor();
  await a.select(); await a.page.getByText('Later persisted answer', { exact: true }).waitFor();
  assert.equal(a.counts().historyCalls, 2); a.check();
});
test('confirmed blank conversation attaches to an existing running stream without resume', async t => {
  const a = await app(t, { runs: [running] });
  await a.select(); await a.page.getByText('Attached live answer', { exact: true }).waitFor();
  assert.equal(a.counts().attachCalls, 1); a.check();
});
test('pending history and unmarked saved user prompt cannot trigger blank fallback', async t => {
  const pending = deferred();
  const a = await app(t, { runs: [running], history: () => pending.promise });
  await a.select();
  await a.page.waitForTimeout(750); assert.equal(a.counts().attachCalls, 0);
  pending.resolve([message('Previously sent prompt', 'server-user', { role: 'user', meta: { conversation_id: 'c' } })]);
  await a.page.getByText('Previously sent prompt', { exact: true }).waitFor();
  await a.page.waitForTimeout(750); assert.equal(a.counts().attachCalls, 0); a.check();
});
test('late hydration preserves the newer attached answer and completed state', async t => {
  const pending = deferred();
  const a = await app(t, { runs: [running], history: count => count === 1 ? Promise.resolve([]) : pending.promise });
  await a.select(); await a.page.getByText('Attached live answer', { exact: true }).waitFor();
  await a.select();
  pending.resolve([message('Stale persisted answer'), message('Earlier persisted turn', 'earlier', { created_at: 1 })]);
  await a.page.getByText('Earlier persisted turn', { exact: true }).waitFor();
  await a.page.getByText('Attached live answer', { exact: true }).waitFor();
  assert.equal(await a.page.getByText('Stale persisted answer', { exact: true }).count(), 0);
  assert.equal(a.counts().historyCalls, 2); assert.equal(a.counts().attachCalls, 1); a.check();
});
