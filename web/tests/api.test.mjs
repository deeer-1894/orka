import { createRequire } from 'node:module';
import { join } from 'node:path';
import { test } from 'node:test';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url);
const { api, auth, setOnUnauthorized } = require(join(process.env.ORKA_TEST_BUILD, 'api.js'));

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

test('an endpoint-specific 401 does not clear a still-valid session', async (t) => {
  const storage = new Map([['orka.token', 'valid-token']]);
  const previous = globalThis.localStorage;
  globalThis.localStorage = {
    getItem: key => storage.get(key) || '',
    setItem: (key, value) => storage.set(key, value),
    removeItem: key => storage.delete(key),
  };
  let calls = 0; let expired = 0;
  setOnUnauthorized(() => { expired++; });
  t.mock.method(globalThis, 'fetch', async () => {
    calls++;
    if (calls === 1) return new Response('{}', { status: 401 });
    return new Response(JSON.stringify({ code: 0, data: { email: 'a@test.com', name: 'A' } }), { status: 200 });
  });
  try {
    await assert.rejects(api.listConversations(), /unauthorized/);
    assert.equal(auth.token(), 'valid-token');
    assert.equal(expired, 0);
    assert.equal(calls, 2);
  } finally {
    setOnUnauthorized(() => {});
    if (previous === undefined) delete globalThis.localStorage; else globalThis.localStorage = previous;
  }
});

test('a 401 confirmed by auth probe clears the session once', async (t) => {
  const storage = new Map([['orka.token', 'expired-token']]);
  const previous = globalThis.localStorage;
  globalThis.localStorage = {
    getItem: key => storage.get(key) || '',
    setItem: (key, value) => storage.set(key, value),
    removeItem: key => storage.delete(key),
  };
  let expired = 0;
  setOnUnauthorized(() => { expired++; });
  t.mock.method(globalThis, 'fetch', async () => new Response('{}', { status: 401 }));
  try {
    await assert.rejects(api.listConversations(), /unauthorized/);
    assert.equal(auth.token(), '');
    assert.equal(expired, 1);
  } finally {
    setOnUnauthorized(() => {});
    if (previous === undefined) delete globalThis.localStorage; else globalThis.localStorage = previous;
  }
});

test('an old auth probe cannot expire a newly selected session', async (t) => {
  const storage = new Map([['orka.token', 'old-token']]);
  const previous = globalThis.localStorage;
  globalThis.localStorage = {
    getItem: key => storage.get(key) || '',
    setItem: (key, value) => storage.set(key, value),
    removeItem: key => storage.delete(key),
  };
  let releaseOldProbe;
  const oldProbe = new Promise(resolve => { releaseOldProbe = resolve; });
  let calls = 0; let expired = 0;
  setOnUnauthorized(() => { expired++; });
  t.mock.method(globalThis, 'fetch', async (_url, options) => {
    calls++;
    if (calls === 1 || calls === 3) return new Response('{}', { status: 401 });
    if (calls === 2) return oldProbe;
    assert.equal(options.headers.Authorization, 'Bearer new-token');
    return new Response(JSON.stringify({ code: 0, data: { email: 'new@test.com' } }), { status: 200 });
  });
  try {
    const oldRequest = api.listConversations();
    await new Promise(resolve => setTimeout(resolve, 0));
    auth.set('new-token');
    const newRequest = api.listConversations();
    releaseOldProbe(new Response('{}', { status: 401 }));
    await assert.rejects(oldRequest, /unauthorized/);
    await assert.rejects(newRequest, /unauthorized/);
    assert.equal(auth.token(), 'new-token');
    assert.equal(expired, 0);
    assert.equal(calls, 4);
  } finally {
    setOnUnauthorized(() => {});
    if (previous === undefined) delete globalThis.localStorage; else globalThis.localStorage = previous;
  }
});
