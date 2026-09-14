// Uses a fresh headless context and mock API for every test; never attaches to
// an existing browser/profile or contacts the real backend/model providers.
import { before, after, test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'vite';
import { chromium } from 'playwright';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const BASE = '/api/v1/controller';
const BASE_URL = 'https://models.example.invalid/v1';
const INITIAL_MODELS = ['vendor/model-beta', 'vendor/model-alpha'];
const MODEL_LIST_LABEL = '可选模型列表（每行一个，也支持逗号分隔）';
let server, browser, origin;

before(async () => {
  server = await createServer({ root, configFile: root + 'vite.config.ts', server: { port: 0, host: '127.0.0.1' } });
  await server.listen();
  origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] });
});
after(async () => { await browser?.close(); await server?.close(); });

async function mockApp(t, options = {}) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, acceptDownloads: true, serviceWorkers: 'block' });
  t.after(() => context.close());
  await context.addInitScript(() => localStorage.setItem('orka.token', 'model-ui-test-token'));
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  const requests = [], errors = [], unexpected = [];
  const history = options.history || {};
  const conversations = Object.keys(history).map(id => ({ conversation_id: id, title: id, owner_email: 'model-ui@example.invalid', created_at: Date.now() }));
  let config = { provider: 'custom', base_url: BASE_URL, api_key_set: true, models: [...INITIAL_MODELS], enabled: true };
  const discovered = options.discovered || ['found-z', 'found-a', 'found-z', INITIAL_MODELS[0]];
  await context.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url());
    // The app's web-font stylesheet is also mocked; no external font fetch.
    if (url.origin === 'https://fonts.googleapis.com') return route.fulfill({ contentType: 'text/css', body: '' });
    if (url.origin !== origin) {
      unexpected.push(`external request: ${url.origin}`);
      return route.abort();
    }
    if (!url.pathname.startsWith('/api/')) return route.continue();
    const path = url.pathname.slice(BASE.length);
    const body = request.postDataJSON() || {};
    requests.push({ path, body });
    const json = data => route.fulfill({ json: { code: 0, data } });
    switch (path) {
      case '/auth/me': return json({ email: 'model-ui@example.invalid', name: 'Model UI test' });
      case '/conversation/list': return json(conversations);
      case '/conversation/shared-with-me':
      case '/file/list': return json([]);
      case '/conversation/get-messages': return json(history[body.conversation_id] || []);
      case '/conversation/create-conversation': {
        const c = { conversation_id: `model-ui-${conversations.length}`, title: 'Test model selection', owner_email: 'model-ui@example.invalid', created_at: Date.now() };
        conversations.push(c); return json(c);
      }
      case '/task/get-tasks': return json({ tasks: [], owners: {} });
      case '/skill/list': return json({ skills: [] });
      case '/notification/list': return json({ notifications: [], unread: 0 });
      case '/metrics': return json({ total_tokens: 0 });
      case '/artifact/list': return json({ artifacts: [] });
      case '/artifact/by-conversation': return route.fulfill({ status: 404, json: { code: 404, msg: 'No artifact in this test conversation' } });
      case '/run/list': return json({ runs: [] });
      case '/events': return route.fulfill({ status: 503, body: '' });
      case '/models': return json([
        { version: 'auto', label: 'Auto', hint: `默认 ${config.models[0]}` },
        ...config.models.map(model => ({ version: model, label: model, hint: '手动选择' })),
      ]);
      case '/model-settings/get': return json(config);
      case '/model-settings/discover':
        return options.discoveryFails
          ? route.fulfill({ status: 502, json: { code: 502, msg: 'mock discovery unavailable' } })
          : json({ models: discovered, ...options.discoveryMetadata });
      case '/model-settings/save':
        // Echo only public fields, like the backend; assertions inspect the
        // original request so an accidental legacy field cannot be concealed.
        config = { provider: body.provider, base_url: body.base_url, models: body.models, enabled: body.enabled, api_key_set: !!body.api_key || config.api_key_set };
        return json(config);
      case '/chat/run': {
        // Auto resolves to the first model; followups must use the answer's
        // recorded model, which can differ from the header's "auto" selection.
        const modelVersion = body.selected_version === 'auto' ? config.models[0] : body.selected_version;
        const meta = { conversation_id: body.conversation_id, model_version: modelVersion, model_profile: 'test-profile-1' };
        const id = `answer-${requests.length}`, ts = Date.now();
        const frames = [
          { id: `${id}-stream`, type: 'stream', role: 'assistant', content: `Streaming response for: ${body.message}`, ts, meta },
          { id, type: 'chat', role: 'assistant', content: `Final response for: ${body.message}`, ts, meta },
          { id: `${id}-done`, type: 'task', action: 'done', ts, meta },
        ];
        return route.fulfill({ contentType: 'text/event-stream', body: frames.map(frame => `data: ${JSON.stringify(frame)}\n\n`).join('') });
      }
      case '/chat/followups': return json({ suggestions: [`Follow up on: ${body.prompt}`] });
      default:
        unexpected.push(path);
        return route.fulfill({ status: 500, json: { code: 500, msg: `Unmocked test endpoint: ${path}` } });
    }
  });
  page.on('pageerror', e => errors.push(e.message));
  const catalog = page.waitForResponse(r => new URL(r.url()).pathname === BASE + '/models');
  await page.goto(origin);
  await catalog;
  await page.getByRole('button', { name: '模型配置', exact: true }).waitFor();
  return {
    page, requests,
    async openSettings() {
      await page.getByRole('button', { name: '模型配置', exact: true }).click();
      const dialog = page.getByRole('dialog', { name: '模型配置', exact: true });
      await dialog.getByLabel(MODEL_LIST_LABEL, { exact: true }).waitFor();
      return dialog;
    },
    check() { assert.deepEqual(errors, [], 'no uncaught browser errors'); assert.deepEqual(unexpected, [], 'all API requests must be mocked'); },
  };
}

async function saveSettings(page, dialog) {
  const response = page.waitForResponse(r => new URL(r.url()).pathname === BASE + '/model-settings/save');
  await dialog.getByRole('button', { name: '保存配置', exact: true }).click();
  const result = await response;
  assert.equal(result.status(), 200);
  await dialog.getByRole('status').filter({ hasText: '配置已保存' }).waitFor();
  return result.request().postDataJSON();
}

async function menuLabels(page) {
  await page.getByRole('button', { name: '选择模型', exact: true }).click();
  await page.getByRole('menu').waitFor();
  return page.getByRole('menuitem').evaluateAll(items => items.map(item => item.querySelector('span').textContent));
}

async function sendMessage(page, message, expectedModelVersion) {
  const followups = page.waitForRequest(r => new URL(r.url()).pathname === BASE + '/chat/followups' && r.postDataJSON()?.prompt === message);
  const sent = page.waitForRequest(r => new URL(r.url()).pathname === BASE + '/chat/run' && r.postDataJSON()?.message === message);
  const response = page.waitForResponse(r => new URL(r.url()).pathname === BASE + '/chat/run' && r.request().postDataJSON()?.message === message);
  await page.locator('textarea').fill(message);
  await page.locator('textarea').press('Enter');
  const request = await sent;
  await (await response).finished();
  const followup = (await followups).postDataJSON();
  assert.deepEqual(followup, {
    prompt: message,
    answer: `Final response for: ${message}`,
    selected_version: expectedModelVersion,
    model_profile: 'test-profile-1',
  }, 'followups retain the final assistant model and opaque profile');
  await page.getByRole('button').filter({ hasText: `Follow up on: ${message}` }).waitFor();
  assert.equal(await page.getByText(`Final response for: ${message}`, { exact: true }).count(), 1);
  assert.equal(await page.getByText(`Streaming response for: ${message}`, { exact: true }).count(), 0);
  // A terminal task event restores the send button before another send.
  await page.getByRole('button', { name: '停止', exact: true }).waitFor({ state: 'hidden' });
  return request.postDataJSON();
}

test('model settings expose one ordered list with no primary or secondary fields', async t => {
  const app = await mockApp(t);
  const dialog = await app.openSettings();
  assert.equal(await dialog.getByLabel(MODEL_LIST_LABEL, { exact: true }).inputValue(), INITIAL_MODELS.join('\n'));
  assert.deepEqual(await dialog.locator('label[for]').allTextContents(), ['厂商', 'Base URL', 'API Key', MODEL_LIST_LABEL]);
  assert.equal(await dialog.getByLabel(/主模型|次模型|轻量模型|mini_model|primary|secondary/i).count(), 0);
  assert.doesNotMatch(await dialog.innerText(), /主模型|次模型|轻量模型/);
  assert.equal(await dialog.getByLabel('API Key', { exact: true }).inputValue(), '');
  assert.equal(await dialog.getByLabel('API Key', { exact: true }).getAttribute('type'), 'password');
  app.check();
});

test('model discovery preserves list order and save persists manual ordering without legacy roles', async t => {
  const app = await mockApp(t);
  const { page, requests } = app;
  const dialog = await app.openSettings();
  await dialog.getByRole('button', { name: '获取模型列表', exact: true }).click();
  await dialog.getByRole('status').filter({ hasText: '获取到' }).waitFor();
  const merged = [...INITIAL_MODELS, 'found-z', 'found-a'];
  assert.equal(await dialog.getByLabel(MODEL_LIST_LABEL, { exact: true }).inputValue(), merged.join('\n'));
  assert.equal(requests.filter(r => r.path === '/model-settings/save').length, 0, 'discovery does not silently save');
  const discovery = requests.find(r => r.path === '/model-settings/discover').body;
  assert.deepEqual(discovery, { provider: 'custom', base_url: BASE_URL });
  const saved = await saveSettings(page, dialog);
  assert.deepEqual(saved, { provider: 'custom', base_url: BASE_URL, enabled: true, models: merged });
  await dialog.getByLabel(MODEL_LIST_LABEL, { exact: true }).fill(' found-a, vendor/model-beta\nfound-z\nfound-a\n vendor/model-alpha ');
  const reordered = await saveSettings(page, dialog);
  const orderedModels = ['found-a', INITIAL_MODELS[0], 'found-z', INITIAL_MODELS[1]];
  assert.deepEqual(reordered.models, orderedModels);
  assert.deepEqual(Object.keys(reordered).sort(), ['base_url', 'enabled', 'models', 'provider']);
  assert.deepEqual(requests.filter(r => ['/model-settings/discover', '/model-settings/save'].includes(r.path)).map(r => r.path), ['/model-settings/discover', '/model-settings/save', '/model-settings/save']);
  await dialog.getByRole('button', { name: '关闭模型配置', exact: true }).click();
  assert.deepEqual(await menuLabels(page), ['Auto', ...orderedModels]);
  await page.keyboard.press('Escape');
  const reopened = await app.openSettings();
  assert.equal(await reopened.getByLabel(MODEL_LIST_LABEL, { exact: true }).inputValue(), orderedModels.join('\n'));
  app.check();
});

test('header choices send exact versions and followups preserve the final assistant model', async t => {
  const app = await mockApp(t);
  const { page } = app;
  assert.match(await page.getByRole('button', { name: '选择模型', exact: true }).innerText(), /^Auto/);
  assert.deepEqual(await menuLabels(page), ['Auto', ...INITIAL_MODELS]);
  await page.keyboard.press('Escape');
  const automatic = await sendMessage(page, 'test Auto model', INITIAL_MODELS[0]);
  assert.equal(automatic.selected_version, 'auto');
  for (const model of [...INITIAL_MODELS].reverse()) {
    await menuLabels(page);
    await page.getByRole('menuitem').filter({ has: page.getByText(model, { exact: true }) }).click();
    assert.ok((await page.getByRole('button', { name: '选择模型', exact: true }).innerText()).startsWith(model));
    const manual = await sendMessage(page, `test manual ${model}`, model);
    assert.equal(manual.selected_version, model);
    assert.equal(manual.conversation_id, automatic.conversation_id);
  }
  await menuLabels(page);
  await page.getByRole('menuitem').filter({ has: page.getByText('Auto', { exact: true }) }).click();
  assert.equal((await sendMessage(page, 'test back to Auto', INITIAL_MODELS[0])).selected_version, 'auto');
  app.check();
});

test('exported JSON and its downloaded file omit typed credentials and retired roles', async t => {
  const app = await mockApp(t);
  const { page, requests } = app;
  const dialog = await app.openSettings();
  const secret = 'mock-key-must-never-appear-in-export';
  await dialog.getByLabel('API Key', { exact: true }).fill(secret);
  await dialog.locator('summary').filter({ hasText: '配置文件' }).click();
  const downloading = page.waitForEvent('download');
  await dialog.getByRole('button', { name: '导出配置', exact: true }).click();
  const download = await downloading;
  assert.equal(download.suggestedFilename(), 'orka-model.json');
  const stream = await download.createReadStream();
  assert.ok(stream);
  const chunks = [];
  for await (const chunk of stream) chunks.push(chunk);
  const exported = Buffer.concat(chunks).toString('utf8');
  assert.equal(exported, await dialog.getByRole('textbox', { name: 'JSON 配置', exact: true }).inputValue());
  assert.deepEqual(JSON.parse(exported), { version: 2, provider: 'custom', base_url: BASE_URL, models: INITIAL_MODELS, enabled: true });
  assert.ok(!exported.includes(secret));
  assert.doesNotMatch(exported, /api_key|mini_model|"model"|primary|secondary/);
  assert.equal(requests.filter(r => ['/model-settings/save', '/model-settings/discover'].includes(r.path)).length, 0, 'export neither saves nor sends the key');
  assert.equal(await dialog.getByLabel('API Key', { exact: true }).inputValue(), secret, 'export leaves the unsaved form intact');
  app.check();
});

test('failed discovery keeps manual list entry and saving available', async t => {
  const app = await mockApp(t, { discoveryFails: true });
  const dialog = await app.openSettings();
  await dialog.getByRole('button', { name: '获取模型列表', exact: true }).click();
  await dialog.getByRole('alert').filter({ hasText: '获取模型失败' }).waitFor();
  assert.equal(await dialog.getByLabel(MODEL_LIST_LABEL, { exact: true }).inputValue(), INITIAL_MODELS.join('\n'));
  await dialog.getByLabel(MODEL_LIST_LABEL, { exact: true }).fill('manual-only-model');
  assert.deepEqual((await saveSettings(app.page, dialog)).models, ['manual-only-model']);
  app.check();
});


test('history without a model_profile does not request followups or reuse the previous profile', async t => {
  const messages = (conversationID, profile) => {
    const meta = { conversation_id: conversationID, model_version: INITIAL_MODELS[1], ...(profile === undefined ? {} : { model_profile: profile }) };
    return [
      { id: `${conversationID}-user`, type: 'chat', role: 'user', content: `Prompt from ${conversationID}`, ts: 1, meta: { conversation_id: conversationID } },
      { id: `${conversationID}-assistant`, type: 'chat', role: 'assistant', content: `Answer from ${conversationID}`, ts: 2, meta },
      { id: `${conversationID}-done`, type: 'task', action: 'done', ts: 3, meta: { conversation_id: conversationID } },
    ];
  };
  const app = await mockApp(t, { history: {
    'history-with-profile': messages('history-with-profile', 'test-profile-1'),
    'history-without-profile': messages('history-without-profile'),
    'history-empty-profile': messages('history-empty-profile', ''),
  } });
  const { page, requests } = app;
  // Positive control: the same history hydration path requests suggestions
  // when the saved answer actually supplies a profile.
  const positive = page.waitForRequest(r => new URL(r.url()).pathname === BASE + '/chat/followups');
  await page.getByText('history-with-profile', { exact: true }).click();
  assert.deepEqual((await positive).postDataJSON(), {
    prompt: 'Prompt from history-with-profile', answer: 'Answer from history-with-profile',
    selected_version: INITIAL_MODELS[1], model_profile: 'test-profile-1',
  });
  await page.getByRole('button').filter({ hasText: 'Follow up on: Prompt from history-with-profile' }).waitFor();
  const countBeforeLegacy = requests.filter(r => r.path === '/chat/followups').length;
  for (const conversationID of ['history-without-profile', 'history-empty-profile']) {
    await page.getByText(conversationID, { exact: true }).click();
    await page.getByText(`Answer from ${conversationID}`, { exact: true }).waitFor();
    // Let hydration and passive effects settle, then force another UI render
    // by selecting a model. Neither should send an unscoped historical Q&A.
    await menuLabels(page);
    await page.getByRole('menuitem').filter({ has: page.getByText(INITIAL_MODELS[0], { exact: true }) }).click();
    await new Promise(resolve => setTimeout(resolve, 300));
    assert.equal(requests.filter(r => r.path === '/chat/followups').length, countBeforeLegacy, conversationID);
    assert.equal(await page.getByRole('button').filter({ hasText: 'Follow up on:' }).count(), 0, 'previous suggestions are cleared');
    assert.equal(requests.filter(r => r.path === '/chat/run').length, 0, 'history viewing does not start a run');
  }
  app.check();
});


test('provider presets populate the endpoint and label fallback models without claiming key validation', async t => {
  const notice = '此地址未提供模型列表接口，已提供厂商预设候选。尚未验证密钥和模型调用权限。';
  const app = await mockApp(t, { discovered: ['doubao-seed-2.0-pro'], discoveryMetadata: { source: 'preset', notice } });
  const dialog = await app.openSettings();
  await dialog.getByLabel('厂商', { exact: true }).selectOption('volcengine-plan');
  assert.equal(await dialog.getByLabel('Base URL', { exact: true }).inputValue(), 'https://ark.cn-beijing.volces.com/api/plan/v3');
  assert.equal(await dialog.getByLabel(MODEL_LIST_LABEL, { exact: true }).inputValue(), '');
  await dialog.getByLabel('API Key', { exact: true }).fill('test-only-secret');
  await dialog.getByRole('button', { name: '获取模型列表', exact: true }).click();
  await dialog.getByRole('status').filter({ hasText: notice }).waitFor();
  assert.equal(await dialog.getByLabel(MODEL_LIST_LABEL, { exact: true }).inputValue(), 'doubao-seed-2.0-pro');
  const saved = await saveSettings(app.page, dialog);
  assert.equal(saved.provider, 'volcengine-plan');
  assert.deepEqual(saved.models, ['doubao-seed-2.0-pro']);
  assert.equal(saved.api_key, 'test-only-secret');
  app.check();
});
