import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const { viewFromHash, hashOf } = require(`${process.env.ORKA_TEST_BUILD}/lib/view.js`);

test('views round-trip through the hash and reject malformed ids', () => {
  for (const v of [{ kind: 'chat' }, { kind: 'tasks' }, { kind: 'project', id: 'prj_ab-1' }]) assert.deepEqual(viewFromHash(hashOf(v)), v);
  assert.deepEqual(viewFromHash('#project/../x'), { kind: 'chat' });
  assert.deepEqual(viewFromHash('#nope'), { kind: 'chat' });
});
