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

async function openEditor(t, task) {
  const page = await browser.newPage();
  t.after(() => page.close());
  const saved = [];
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/task/save')) {
      const body = route.request().postDataJSON();
      saved.push(body);
      return route.fulfill({ json: { code: 0, data: { task_id: body.task_id || 't-new', variables: {}, cron_status: 'on', webhook_token: 'tok', tool_policy: body.tool_policy } } });
    }
    return route.fulfill({ json: { code: 0, data: {} } });
  });
  await page.goto(origin + '/tests/task-policy.html' + (task ? '?task=' + encodeURIComponent(JSON.stringify(task)) : ''));
  await page.getByRole('group', { name: '自动运行时允许' }).waitFor();
  return { page, saved };
}

const box = (page, label) => page.getByRole('checkbox', { name: new RegExp('^' + label) });

test('a new event task defaults to web search plus workspace writes and saves what the user picks', async t => {
  const { page, saved } = await openEditor(t);
  assert.equal(await box(page, '写入工作区文件').isChecked(), true);
  assert.equal(await box(page, '执行代码').isChecked(), false);
  assert.equal(await box(page, '操作浏览器').isChecked(), false);
  assert.equal(await box(page, '读取工作区文件').isChecked(), false);
  assert.equal(await page.getByTestId('task-policy-summary').textContent(), '联网检索 + 写入工作区文件');
  assert.match(await page.getByRole('group', { name: '自动运行时允许' }).textContent(), /请求内容可能夹带指令/);

  await box(page, '写入工作区文件').uncheck();
  assert.equal(await page.getByTestId('task-policy-summary').textContent(), '联网检索');
  await page.getByRole('button', { name: '保存' }).click();
  await page.getByRole('heading', { name: '任务已创建' }).waitFor();
  assert.equal(saved.length, 1);
  assert.deepEqual(saved[0].tool_policy, { allow: [] });
});

test('reading private data and reaching the network exclude each other, and the order stays stable', async t => {
  const task = { task_id: 't1', variables: { title: 'Deploy', prompt_template: '部署后检查' }, cron_status: 'on', retry_count: 0,
    trigger: { kind: 'schedule', cron: '0 9 * * *' }, tool_policy: { allow: ['browser'] } };
  const { page, saved } = await openEditor(t, task);
  assert.equal(await box(page, '写入工作区文件').isChecked(), false);
  assert.equal(await box(page, '操作浏览器').isChecked(), true);
  assert.match(await page.getByRole('group', { name: '自动运行时允许' }).textContent(), /网页内容可能夹带指令/);
  // Code reads the workspace, so it takes the browser (the network) away.
  await box(page, '执行代码').check();
  assert.equal(await box(page, '操作浏览器').isChecked(), false);
  await box(page, '写入工作区文件').check();
  await box(page, '读取工作区文件').check();
  assert.equal(await page.getByTestId('task-policy-summary').textContent(), '不联网 + 写入工作区文件、读取工作区文件、执行代码');
  // And the browser takes both private reads away again.
  await box(page, '操作浏览器').check();
  assert.equal(await box(page, '执行代码').isChecked(), false);
  assert.equal(await box(page, '读取工作区文件').isChecked(), false);
  await page.getByRole('button', { name: '保存' }).click();
  await page.waitForFunction(() => document.title === 'closed');
  assert.deepEqual(saved[0].tool_policy, { allow: ['workspace_write', 'browser'] });
});

test('a task saved before policies existed shows the default the server enforces', async t => {
  const task = { task_id: 't2', variables: { prompt_template: '旧任务' }, cron_status: 'off', trigger: { kind: 'event' } };
  const { page } = await openEditor(t, task);
  assert.equal(await page.getByTestId('task-policy-summary').textContent(), '联网检索 + 写入工作区文件');
});
