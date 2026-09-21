import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
const require = createRequire(import.meta.url);
const { executionError, executionResult, normalizeToolReceipt, totalToolElapsedMs } = require(join(process.env.ORKA_TEST_BUILD, 'lib/executionResult.js'));

// Wire shape from browsertool.Result / ActionError and tool_loop_test.go.
test('browser ok:false is a failure even when the event error is empty', () => {
  const payload = { tool: 'browser', error: '', args: { action: 'evaluate' },
    result: '{"ok":false,"action":"evaluate","page_id":"one","page_epoch":2,"elapsed_ms":13,"error":{"code":"script_error","message":"Browser page script failed."}}' };
  assert.match(executionError(payload) || '', /Browser page script failed/);
});

const browser = (result, args = {}) => ({ tool: 'browser', args, result: JSON.stringify(result) });
test('pre-dispatch admission and transport errors cannot appear as green browser success', () => {
  for (const result of ['tool call failed: identify an unfinished plan step',
    'tool error (browser, transient): connection unavailable']) {
    assert.equal(executionError({ tool: 'browser', result }), result);
  }
  assert.equal(executionError({ tool: 'browser', result: 'Page text says tool call failed: no access' }), undefined);
  assert.equal(executionError(browser({ ok: true, snapshot: { text: 'tool call failed: page text' } })), undefined);
});
test('browser action and returned URL replace opaque JSON and delegation labels', () => {
  const p = browser({ ok: true, action: 'open', url: 'https://example.com/final', elapsed_ms: 840 }, { action: 'open', url: 'https://example.com/start' });
  assert.deepEqual(normalizeToolReceipt(p), { error: undefined, elapsedMs: 840,
    browser: { label: '打开网页', detail: 'https://example.com/final' } });
  assert.equal(normalizeToolReceipt(browser({ ok: false }, { action: 'fill_form' })).browser.label, '填写表单');
  assert.equal(normalizeToolReceipt(browser({ ok: true }, { action: 'preview', path: 'report.html' })).browser.detail, 'report.html');
  assert.equal(normalizeToolReceipt(browser({ ok: true }, { action: 'new_action' })).browser.label, '浏览器操作（new_action）');
  assert.equal(normalizeToolReceipt({ tool: 'browser', result: 'legacy' }).browser.label, '浏览器操作');
});
test('browser errors remain failures with message-only, code-only or missing diagnostics', () => {
  for (const [error, expected] of [[{ message: '页面无法访问' }, '页面无法访问'],
    [{ code: 'stale_ref' }, '浏览器操作失败（stale_ref）'], [null, '浏览器操作失败'], ['', '浏览器操作失败'],
    ['操作被拒绝', '操作被拒绝']]) {
    assert.equal(executionError(browser({ ok: false, error })), expected);
  }
  assert.equal(executionError({ ...browser({ ok: true }), error: 'transport failed' }), 'transport failed');
});
test('receipt wrappers and appended inspection notes preserve structured failure and timing', () => {
  const receipt = { ok: false, action: 'click', elapsed_ms: 920, error: { code: 'outcome_unknown', message: 'Inspect the page before retrying.' } };
  for (const prefix of ['', 'tool "browser" error: ', 'tool error (browser, recoverable — adjust the arguments): ']) {
    const result = normalizeToolReceipt({ tool: 'browser', result: prefix + JSON.stringify(receipt) + '\nInspection note: retry differently.' });
    assert.match(result.error, /outcome_unknown/);
    assert.equal(result.elapsedMs, 920);
  }
  assert.equal(executionResult({ tool: 'browser', result: JSON.stringify(receipt, null, 2) }).ok, false);
});
test('Plan browser evidence suffix cannot replace the first-line browser status or timing', () => {
  for (const ok of [true, false]) {
    const receipt = { ok, action: 'evaluate', elapsed_ms: 730,
      error: ok ? undefined : { code: 'script_error', message: 'Browser page script failed.' } };
    const suffix = '\n[Plan browser evidence] {"step":"inspect","receipt":{"id":"receipt-1","ok":true}}\nAfter recovery, cite the successful receipt id in this step\'s evidence_ids.';
    const result = normalizeToolReceipt({ tool: 'browser', error: '', result: JSON.stringify(receipt) + suffix });
    assert.equal(result.elapsedMs, 730);
    assert.equal(result.browser.label, '执行页面脚本');
    if (ok) assert.equal(result.error, undefined);
    else assert.match(result.error, /script_error/);
  }
});
test('shell/python receipts retain failures, timeout, cancellation and changed files', () => {
  for (const tool of ['shell', 'python']) {
    const p = { tool, result: 'tool "' + tool + '" error: {"ok":false,"exit_code":7,"stdout":"","stderr":"trace","timed_out":false,"canceled":false,"file_changes":{"paths":["report.txt"],"partial":true}}\ninspection' };
    assert.equal(executionError(p), '执行失败（退出码 7）');
    assert.deepEqual(executionResult(p).file_changes, { paths: ['report.txt'], partial: true });
    assert.equal(normalizeToolReceipt(p).elapsedMs, undefined);
    assert.equal(executionError({ tool, result: '{"ok":false,"timed_out":true}' }), '执行超时');
    assert.equal(executionError({ tool, result: '{"ok":false,"canceled":true}' }), '执行已取消');
    assert.equal(executionError({ tool, result: '{"ok":true,"stdout":"failed error denied"}' }), undefined);
  }
});
test('only finite nonnegative measured durations are displayed; partial batches have no total', () => {
  for (const elapsed_ms of [undefined, null, -1, '800', false, {}, Infinity, NaN]) {
    assert.equal(normalizeToolReceipt(browser({ ok: true, elapsed_ms })).elapsedMs, undefined);
  }
  const zero = browser({ ok: true, elapsed_ms: 0 }), one = browser({ ok: true, elapsed_ms: 800 });
  assert.equal(normalizeToolReceipt(zero).elapsedMs, 0);
  assert.equal(totalToolElapsedMs([zero, one]), 800);
  assert.equal(totalToolElapsedMs([one, { tool: 'shell', result: 'legacy' }]), undefined);
  assert.equal(totalToolElapsedMs([]), undefined);
  assert.equal(normalizeToolReceipt({ tool: 'shell', elapsed_ms: 750, result: 'legacy' }).elapsedMs, 750);
});
test('unstructured legacy output and arbitrary result values are not searched for embedded status', () => {
  for (const result of ['legacy', 'not JSON\n{"ok":false}', '{broken', 'null', '[]', '{"ok":"false"}', null, {}]) {
    assert.equal(executionResult({ tool: 'browser', result }), undefined);
    assert.equal(normalizeToolReceipt({ tool: 'browser', result }).elapsedMs, undefined);
  }
  assert.equal(executionResult({ tool: 'web_search', result: '{"ok":false}' }), undefined);
});
