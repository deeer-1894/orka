import { before, after, test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'vite';
import { chromium } from 'playwright';
import { fileURLToPath } from 'node:url';

let server, browser, origin;
before(async () => {
  server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { port: 0, host: '127.0.0.1' } });
  await server.listen();
  origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] });
});
after(async () => { await browser?.close(); await server?.close(); });

test('new chats remember explicit choices, preserve existing conversations and isolate accounts', async t => {
  const page = await browser.newPage();
  t.after(() => page.close());
  await page.goto(origin + '/tests/chat-preferences.html');
  const read = async () => JSON.parse(await page.locator('output').textContent());
  await page.getByLabel('model').fill('glm-5.3-flash');
  await page.getByText('Toggle confirmation', { exact: true }).click();
  await page.getByText('Choose tools', { exact: true }).click();
  await page.getByText('Create first', { exact: true }).click();
  await page.getByText('New chat', { exact: true }).click();
  assert.deepEqual(await read(), { cid: '', selectedVersion: 'glm-5.3-flash', confirmRisky: false, enabledTools: [], activeSkill: null });
  await page.getByLabel('model').fill('glm-5.3');
  await page.getByText('Open first', { exact: true }).click();
  assert.equal((await read()).selectedVersion, 'glm-5.3-flash');
  // Changing just approval in an old conversation must not overwrite the
  // independently remembered model selection for the next new conversation.
  await page.getByText('Toggle confirmation', { exact: true }).click();
  await page.getByText('New chat', { exact: true }).click();
  assert.equal((await read()).selectedVersion, 'glm-5.3');
  assert.equal((await read()).confirmRisky, true);
  await page.reload();
  await page.getByLabel('model').waitFor();
  assert.equal((await read()).selectedVersion, 'glm-5.3');
  await page.goto(origin + '/tests/chat-preferences.html?owner=two@example.com');
  await page.getByLabel('model').waitFor();
  assert.equal((await read()).selectedVersion, 'auto');
  assert.equal((await read()).confirmRisky, true);
  await page.goto(origin + '/tests/chat-preferences.html');
  await page.getByLabel('model').waitFor();
  assert.equal((await read()).selectedVersion, 'glm-5.3');
});
