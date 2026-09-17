import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
const { workspaceLinkPath, sessionFileCandidates, existingSessionFiles, normalizeWorkspacePath } = createRequire(import.meta.url)(join(process.env.ORKA_TEST_BUILD, 'lib/sessionFiles.js'));
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
  const candidates = sessionFileCandidates([tool('update_plan', { outputs: ['F/deep/nested/manifest.json', 'F/delivery.zip'] }), tool('shell', { command: 'python3 make.py' }, 'Generated: F/deep/nested/chart.svg\nSaved: /home/aibox/.orka/storage/me@example.com/sessions/f/F/dashboard.html')], ctx);
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

test('current deliverables stay visible ahead of intermediate files but require existence', async () => {
 const messages = Array.from({length:12},(_,i)=>tool('file_write',{path:`source-${i}.py`}));
 messages.push(tool('update_plan',{outputs:['release.zip','validation.md']}));
 const candidates=sessionFileCandidates(messages,ctx);
 assert.deepEqual(candidates.slice(0,2),['release.zip','validation.md']);
 const found=await existingSessionFiles(candidates,async()=>candidates.filter(p=>p!=='validation.md').map(name=>({name,dir:false})));
 assert.equal(found[0],'release.zip');assert.ok(!found.includes('validation.md'));
});
test('steering an active task preserves its declared deliverable priority', () => {
  const messages = [
    tool('file_write', { path: 'source.py' }),
    { ...tool('update_plan', { outputs: ['release.zip'] }), meta: { conversation_id: 'f', run_id: 'active' } },
    msg('chat', null, { role: 'user', action: 'human_input', content: '补充发布回归', meta: { conversation_id: 'f', run_id: 'active' } }),
    tool('file_write', { path: 'tests.py' }),
  ];
  assert.deepEqual(sessionFileCandidates(messages, ctx), ['release.zip', 'source.py', 'tests.py']);
  messages.push(msg('chat', null, { role: 'user', action: 'human_input', content: '另一个任务', meta: { conversation_id: 'f', run_id: 'next' } }));
  assert.deepEqual(sessionFileCandidates(messages, ctx), ['source.py', 'tests.py']);
});
test('finished explicit deliverable links are accepted but generic link mentions are not', () => {
  assert.deepEqual(sessionFileCandidates([msg('chat', null, { content: '已生成：[报告](F/report.md)' }), msg('chat', null, { content: '可能参考 [旧报告](B/report.md)' })], ctx), ['F/report.md']);
});

test('successful script output may explicitly return a full artifact path without a prose label', () => {
  assert.deepEqual(sessionFileCandidates([tool('shell', { command: 'python3 generate.py' }, '/home/aibox/.orka/storage/me@example.com/sessions/f/F/result.csv')], ctx), ['F/result.csv']);
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


test('absolute session paths strip only the current session root and reject a foreign session', () => {
  const normalize = p => normalizeWorkspacePath(p, ctx.ownerEmail, ctx.conversationID);
  assert.equal(normalize('/home/aibox/.orka/storage/me@example.com/sessions/f/deep/report.csv'), 'deep/report.csv');
  assert.equal(normalize('/home/aibox/.orka/storage/me@example.com/sessions/e/deep/report.csv'), undefined);
  assert.equal(normalize('/home/aibox/.orka/storage/me@example.com/report.csv'), undefined);
  assert.equal(normalize('deep/report.csv'), 'deep/report.csv');
});

test('Markdown file links decode paths and remain scoped to the current conversation', () => {
  assert.equal(workspaceLinkPath('outputs/report.md', ctx), 'outputs/report.md');
  assert.equal(workspaceLinkPath('./outputs/%E6%8A%A5%E5%91%8A%20a%23b.csv#details', ctx), 'outputs/报告 a#b.csv');
  assert.equal(workspaceLinkPath('/storage/me@example.com/sessions/f/outputs/a.py', ctx), 'outputs/a.py');
  for (const href of ['https://docs.github.com/a.md', '//other.test/a.md', 'mailto:a@example.com', '#section', '?page=1', '../other.csv', '%2e%2e/other.csv', '/api/v1/controller/health', '/storage/me@example.com/sessions/foreign/a.csv', 'bad%ZZ.csv']) {
    assert.equal(workspaceLinkPath(href, ctx), undefined, href);
  }
  assert.equal(workspaceLinkPath('outputs/a.csv', { ...ctx, conversationID: '' }), undefined);
});

test('sandbox delivery references resolve only within this conversation workspace', () => {
  for (const href of ['sandbox:?path=/workspace/report.zip', 'sandbox:/workspace/report.zip', '/workspace/report.zip', 'sandbox:report.zip']) {
    assert.equal(workspaceLinkPath(href, ctx), 'report.zip');
  }
  assert.equal(workspaceLinkPath('sandbox:?path=%2Fworkspace%2F%E6%8A%A5%E5%91%8A%23a.zip', ctx), '报告#a.zip');
  for (const href of ['sandbox://host/report.zip', 'sandbox:/workspace/../other.zip', 'sandbox:?path=/etc/passwd', 'sandbox:?path=/workspace/a&path=/workspace/b', 'sandbox:?path=/workspace/a&token=x', 'sandbox:javascript:alert(1)', 'javascript:alert(1)', 'sandbox:?path=/storage/me@example.com/sessions/foreign/a']) {
    assert.equal(workspaceLinkPath(href, ctx), undefined, href);
  }
  assert.deepEqual(sessionFileCandidates([msg('chat', null, { content: '下载：[成果](sandbox:?path=/workspace/report.zip)' })], ctx), ['report.zip']);
});

test('execution metadata exposes archives without stdout declarations, including failed builds', async () => {
 const receipt = { ok: false, exit_code: 7, stdout: '', file_changes: { paths: ['package.zip', 'out/report.html', '../foreign.zip'], partial: false } };
 const candidates = sessionFileCandidates([tool('shell', {command:'build'}, JSON.stringify(receipt))], ctx);
 assert.deepEqual(candidates, ['package.zip', 'out/report.html']);
 const found = await existingSessionFiles(candidates, async dir => dir === '.' ? [{name:'package.zip',dir:false}] : []);
 assert.deepEqual(found, ['package.zip']);
});
test('execution receipts remain readable with appended inspection notes', () => {
 const result = JSON.stringify({ok:true, stdout:'Saved: out/report.csv', file_changes:{paths:['bundle.zip']}}) + '\n\n[Workspace inspection: changed files]';
 assert.deepEqual(sessionFileCandidates([tool('python',{},result)],ctx), ['bundle.zip']);
});


test('failed adapter envelopes keep observed output files accessible', () => {
 const result = 'tool error (shell, recoverable — adjust the arguments, try another tool, or proceed without this result): tool "shell" error: ' + JSON.stringify({ok:false,exit_code:7,stdout:'',stderr:'test failed',file_changes:{paths:['tests.log']}}) + '\n[Execution evidence id: exit_code=7.]';
 assert.deepEqual(sessionFileCandidates([tool('shell',{},result)],ctx), ['tests.log']);
});


test('modern execution metadata takes precedence over filenames printed by read-only commands', () => {
 const result = JSON.stringify({ok:true,exit_code:0,stdout:'Saved: sources/old.md\n.orka_offload/source.txt',file_changes:{paths:[],partial:false}});
 assert.deepEqual(sessionFileCandidates([tool('shell',{command:'python3 inspect.py'},result)],ctx), []);
});
