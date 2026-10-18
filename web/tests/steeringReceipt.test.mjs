import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
const require = createRequire(import.meta.url);
const { steeringReceipt } = require(join(process.env.ORKA_TEST_BUILD, 'lib/steeringReceipt.js'));
const message = { id: 'persisted-steer', type: 'chat', role: 'user', action: 'human_input', content: '追加约束',
  meta: { conversation_id: 'c', run_id: 'r' }, payload: { request_id: 'request-1', file_ids: [] }, ts: 1 };
test('persisted steering acceptance shows joined, without claiming model adoption', () => {
  assert.equal(steeringReceipt(message), '已加入当前任务');
  assert.equal(steeringReceipt(JSON.parse(JSON.stringify(message))), '已加入当前任务');
  assert.equal(steeringReceipt({ ...message, content: '', payload: { request_id: 'request-2', file_ids: ['data.csv'] } }), '已加入当前任务');
});
test('ordinary prompts, local echoes, runtime input and absent request/run identity have no steering acknowledgement', () => {
  for (const patch of [{ action: undefined }, { action: 'runtime_context' }, { role: 'assistant' }, { type: 'tool' },
    { payload: null }, { payload: {} }, { payload: { request_id: '' } }, { payload: { request_id: false } }, { meta: {} }]) {
    assert.equal(steeringReceipt({ ...message, ...patch }), undefined);
  }
});
