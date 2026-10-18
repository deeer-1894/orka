import { createRequire } from 'node:module';
import { join } from 'node:path';
import { test } from 'node:test';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url);
const { exportModelProfile, importModelProfile } = require(join(process.env.ORKA_TEST_BUILD, 'lib/modelSettings.js'));
const config = { provider: 'custom', base_url: 'http://localhost:11434/v1', api_key: 'private-test-key', models: ['a', 'b'], enabled: true };
test('portable profile never exports a credential', () => {
  const json = exportModelProfile(config);
  assert.ok(!json.includes('private-test-key'));
  assert.ok(!json.includes('api_key'));
  assert.deepEqual(importModelProfile(json), { provider: 'custom', base_url: config.base_url, models: ['a', 'b'], enabled: true });
});
test('import never turns a pasted credential into an outbound secret', () => {
  assert.equal(importModelProfile(JSON.stringify(config)).api_key, undefined);
});
test('invalid configuration cannot change form state', () => {
  for (const base_url of ['javascript:alert(1)', 'https://user:pass@example.com/v1', 'https://example.com/v1?api_key=secret', 'https://example.com/v1#fragment']) {
    assert.throws(() => importModelProfile(JSON.stringify({ ...config, base_url })));
  }
  for (const v of [null, [], {}, { ...config, models: [1] }]) assert.throws(() => importModelProfile(JSON.stringify(v)));
});

test('legacy profile migrates default into list without secondary role', () => {
  const result = importModelProfile(JSON.stringify({ ...config, model: 'default', mini_model: 'secondary' }));
  assert.deepEqual(result.models, ['default', 'a', 'b']);
  assert.equal(result.model, undefined);
  assert.equal(result.mini_model, undefined);
});
test('list order sets Auto default and survives roundtrip', () => {
  const result = importModelProfile(exportModelProfile({ ...config, models: ['b', 'a', 'b'] }));
  assert.deepEqual(result.models, ['b', 'a']);
  assert.throws(() => importModelProfile(JSON.stringify({ ...config, models: ['  '] })));
});
