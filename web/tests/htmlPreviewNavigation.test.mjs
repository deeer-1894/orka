import { createRequire } from 'node:module';
import { join } from 'node:path';
import { test } from 'node:test';
import assert from 'node:assert/strict';

const require = createRequire(import.meta.url);
const { previewNavigationURL } = require(join(process.env.ORKA_TEST_BUILD, 'lib/htmlPreviewNavigation.js'));
const frame = {};
const nonce = 'preview-instance';
const event = (url, overrides = {}) => ({ source: frame, data: { kind: 'orka:preview-link', nonce, url }, ...overrides });

test('preview navigation accepts only the current frame and document', () => {
  assert.equal(previewNavigationURL(event('https://github.com/cloudwego/eino/releases'), frame, nonce), 'https://github.com/cloudwego/eino/releases');
  assert.equal(previewNavigationURL(event('https://example.com', { source: {} }), frame, nonce), null);
  assert.equal(previewNavigationURL(event('https://example.com'), frame, 'old-instance'), null);
  assert.equal(previewNavigationURL(event('https://example.com'), null, nonce), null);
  for (const data of [null, {}, { kind: 'other', nonce, url: 'https://example.com' }]) {
    assert.equal(previewNavigationURL(event('', { data }), frame, nonce), null);
  }
});

test('preview navigation rejects executable, local, credentialed and malformed URLs', () => {
  for (const url of ['javascript:alert(1)', 'data:text/html,test', 'file:///etc/passwd', '/relative', 'https://user:secret@example.com', 42, null]) {
    assert.equal(previewNavigationURL(event(url), frame, nonce), null);
  }
});
