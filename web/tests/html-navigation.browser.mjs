import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'vite';
import { chromium } from 'playwright';
import { fileURLToPath } from 'node:url';

test('HTML report citations open separately while the preview stays isolated', async t => {
  const root = fileURLToPath(new URL('../', import.meta.url));
  const server = await createServer({ root, server: { port: 0, host: '127.0.0.1' } });
  await server.listen(); t.after(() => server.close());
  const browser = await chromium.launch({ headless: true, args: ['--no-sandbox'], ignoreDefaultArgs: ['--disable-popup-blocking'] });
  t.after(() => browser.close());
  const context = await browser.newContext();
  const page = await context.newPage();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  const source = 'https://github.com/cloudwego/eino/releases';
  await context.route('https://github.com/**', route => route.fulfill({ body: '<h1>Official source navigation fixture</h1>', contentType: 'text/html' }));
  await page.route('**/api/**', route => {
    if (new URL(route.request().url()).pathname.endsWith('/file/download')) return route.fulfill({ body: `<!doctype html><h1>Report</h1><a href="${source}" target="_blank">Official releases</a><output id="scope"></output><output id="automatic"></output><script>
      try { parent.localStorage.getItem('orka.token'); document.getElementById('scope').textContent='leaked'; }
      catch { document.getElementById('scope').textContent='isolated'; }
      document.getElementById('automatic').textContent=window.open('https://github.com/automatic','_blank')===null?'blocked':'opened';
      </script>`, contentType: 'text/html' });
    return route.fulfill({ json: { code: 0, data: [] } });
  });
  await page.goto(origin + '/tests/session-files.html');
  await page.waitForFunction(() => !!window.fileTest);
  await page.evaluate(() => window.fileTest.set({ mode: 'preview', conversationID: 'A', name: 'report.html' }));
  const frame = page.frameLocator('iframe');
  await frame.locator('#scope').getByText('isolated', { exact: true }).waitFor();
  await frame.locator('#automatic').getByText('blocked', { exact: true }).waitFor();
  const contentFrame = page.frames().find(candidate => candidate !== page.mainFrame());
  assert.ok(!(await contentFrame.locator('script').allTextContents()).join('').includes('orka:preview-link'));
  await contentFrame.evaluate(() => {
    document.querySelector('a').click();
    parent.postMessage({ kind: 'orka:preview-link', nonce: 'forged', url: 'https://github.com/forged' }, '*');
  });
  await page.waitForTimeout(100);
  assert.equal(context.pages().length, 1);
  const [popup] = await Promise.all([
    context.waitForEvent('page', { timeout: 2000 }),
    frame.getByRole('link', { name: 'Official releases', exact: true }).click(),
  ]);
  await popup.waitForURL(source);
  assert.equal(await popup.evaluate(() => window.opener), null);
  assert.equal(page.url(), origin + '/tests/session-files.html');
  assert.equal(await frame.locator('#scope').textContent(), 'isolated');
  const sandbox = await page.locator('iframe').getAttribute('sandbox');
  assert.ok(!sandbox.includes('allow-same-origin'));
  assert.ok(!sandbox.includes('allow-popups'));
  assert.ok(!(await page.locator('iframe').getAttribute('srcdoc')).includes('token='));
});
