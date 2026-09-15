import { createRequire } from 'node:module';
import { join } from 'node:path';
import { test } from 'node:test';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url);
const { api } = require(join(process.env.ORKA_TEST_BUILD, 'api.js'));

test('stopping a conversation does not resolve its id as a scheduled task', async (t) => {
  let captured;
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    captured = { url, body: JSON.parse(options.body) };
    return new Response(JSON.stringify({ code: 0, data: { status: 'killed' } }));
  });
  const previous = globalThis.localStorage;
  globalThis.localStorage = { getItem: () => '' };
  try {
    await api.kill('conversation-123');
    assert.deepEqual(captured, { url: '/api/v1/controller/chat/kill', body: { conversation_id: 'conversation-123' } });
  }
  finally { if (previous === undefined) delete globalThis.localStorage; else globalThis.localStorage = previous; }
});
