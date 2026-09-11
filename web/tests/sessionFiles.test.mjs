import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
const { sessionFileCandidates, existingSessionFiles, normalizeWorkspacePath } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/sessionFiles.js'));
const ctx = { conversationID: 'f', ownerEmail: 'me@example.com' };
const msg = (type, payload, extra = {}) => ({ id: '1', type, role: 'assistant', payload, ts: 1, meta: { conversation_id: 'f' }, ...extra });
const tool = (name, args, result = 'saved successfully', extra = {}) => msg('tool', { tool: name, args, result, ...extra });

test('exact F paths never fall back to B/E basenames', async () => {
  const candidates = sessionFileCandidates([tool('file_write', { path: 'F/report.md' })], ctx);
  const calls = [];
  const found = await existingSessionFiles(candidates, async dir => { calls.push(dir); return dir === 'B' ? [{ name: 'report.md', dir: false }] : []; });
  assert.deepEqual(candidates, ['F/report.md']); assert.deepEqual(found, []); assert.deepEqual(calls, ['F']);
});
test('bare written root path is exact, while thought/prose/stream names and read outputs are not provenance', () => {
  const messages = [tool('file_write', { path: './report.md' }), msg('stream', null, { action: 'reasoning', content: 'F/report.md E/report.md' }), msg('chat', null, { content: '考虑后面生成 F/report.md' }), tool('file_read', { path: 'B/old.md' }, 'B/old.md'), tool('shell', { command: 'ls B' }, 'B/old.md')];
  assert.deepEqual(sessionFileCandidates(messages, ctx), ['report.md']);
});
test('failed/incomplete writes and input filenames do not declare output', () => {
  assert.deepEqual(sessionFileCandidates([tool('file_write', { path: 'F/a.md' }, '', { error: 'denied' }), tool('file_write', { path: 'F/b.md' }, 'Error: permission denied'), msg('tool', { tool: 'file_write', args: { path: 'F/c.md' } }), tool('csv_to_json', { path: 'B/input.csv', out: 'F/result.json' })], ctx), ['F/result.json']);
});
test('explicit shell output paths and current plan outputs are checked even in deep directories', async () => {
  const candidates = sessionFileCandidates([tool('update_plan', { outputs: ['F/deep/nested/manifest.json', 'F/delivery.zip'] }), tool('shell', { command: 'python3 make.py' }, 'Generated: F/deep/nested/chart.svg\nSaved: /home/aibox/.orka/storage/me@example.com/F/dashboard.html')], ctx);
  assert.deepEqual(new Set(candidates), new Set(['F/deep/nested/manifest.json', 'F/delivery.zip', 'F/deep/nested/chart.svg', 'F/dashboard.html']));
  const found = await existingSessionFiles(candidates, async dir => dir === 'F/deep/nested' ? [{ name: 'chart.svg', dir: false }, { name: 'manifest.json', dir: false }] : [{ name: 'dashboard.html', dir: false }, { name: 'delivery.zip', dir: true }]);
  assert.deepEqual(new Set(found), new Set(['F/deep/nested/chart.svg','F/deep/nested/manifest.json','F/dashboard.html']));
});
test('path normalization rejects foreign roots, traversal and remote URLs', () => {
  for (const p of ['../F/a.md', 'F/../../a.md', 'https://x/F/a.md', '//x/a.md', '/tmp/F/a.md', '/storage/other/F/a.md']) assert.equal(normalizeWorkspacePath(p, ctx.ownerEmail), undefined, p);
  assert.equal(normalizeWorkspacePath('./F/a b.md', ctx.ownerEmail), 'F/a b.md');
});
test('conversation filter and new-turn plan boundaries discard unrelated declarations', () => {
  const items = [tool('update_plan', { outputs: ['B/old.md'] }), msg('chat', null, { role: 'user', content: 'new task' }), tool('update_plan', { outputs: ['F/new.md'] }), { ...tool('file_write', { path: 'E/old.md' }), meta: { conversation_id: 'e' } }];
  assert.deepEqual(sessionFileCandidates(items, ctx), ['F/new.md']);
});
test('finished explicit deliverable links are accepted but generic link mentions are not', () => {
  assert.deepEqual(sessionFileCandidates([msg('chat', null, { content: '已生成：[报告](F/report.md)' }), msg('chat', null, { content: '可能参考 [旧报告](B/report.md)' })], ctx), ['F/report.md']);
});

test('successful script output may explicitly return a full artifact path without a prose label', () => {
  assert.deepEqual(sessionFileCandidates([tool('shell', { command: 'python3 generate.py' }, '/home/aibox/.orka/storage/me@example.com/F/result.csv')], ctx), ['F/result.csv']);
});
test('listing failure, duplicate declarations and directories never invent artifacts', async () => {
  const candidates = sessionFileCandidates([tool('update_plan', { outputs: ['F/a.md', 'F/a.md', 'G/a.md'] })], ctx);
  assert.deepEqual(candidates, ['F/a.md', 'G/a.md']);
  const result = await existingSessionFiles(candidates, async dir => { if (dir === 'G') throw new Error('404'); return [{ name: 'a.md', dir: true }, { name: '../a.md', dir: false }]; });
  assert.deepEqual(result, []);
});

test('reasoning metadata never becomes a deliverable declaration even if content claims completion', () => {
  assert.deepEqual(sessionFileCandidates([msg('chat', null, { action: 'reasoning', content: '已生成：[报告](F/report.md)' })], ctx), []);
});

test('structured failures and refusal messages cannot claim an already existing output path', async () => {
  for (const result of ['{"success":false,"message":"write rejected"}', '{"error":"permission denied"}', 'Permission denied: missing scope file:write', '拒绝写入：没有权限', '操作被拒绝', 'tool "file_write" error: permission denied', '{"success":true,"error":"disk full"}']) {
    const candidates = sessionFileCandidates([tool('file_write', { path: 'report.md' }, result)], ctx);
    assert.deepEqual(await existingSessionFiles(candidates, async () => [{ name: 'report.md', dir: false }]), [], result);
  }
  assert.deepEqual(sessionFileCandidates([tool('file_write', { path: 'good.md' }, '{"success":true,"error":null}')], ctx), ['good.md']);
  assert.deepEqual(sessionFileCandidates([tool('file_write', { path: 'report.md' }, 'saved', { success: false })], ctx), []);
});

test('actual confirmation refusal and skipped-operation receipts never claim an existing file', async () => {
  for (const result of ['用户拒绝了该操作,已跳过。', '未收到确认结果,已跳过该操作。', '等待用户确认超时,已跳过该操作。']) {
    const candidates = sessionFileCandidates([tool('file_write', { path: 'report.md' }, result)], ctx);
    assert.deepEqual(candidates, [], result);
    assert.deepEqual(await existingSessionFiles(candidates, async () => [{ name: 'report.md', dir: false }]), [], result);
  }
  assert.deepEqual(sessionFileCandidates([tool('file_write', { path: 'report.md' }, '已确认，saved successfully')], ctx), ['report.md']);
});
