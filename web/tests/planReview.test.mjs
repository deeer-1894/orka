import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const { approvedSteps, approvalMessage, move } = require(`${process.env.ORKA_TEST_BUILD}/lib/planReview.js`);

test('approved steps drop blanks and repeats, keeping order', () => {
  assert.deepEqual(approvedSteps([' 搜索资料 ', '', '写报告', '搜索资料']), ['搜索资料', '写报告']);
});

test('approval message numbers the steps', () => {
  assert.match(approvalMessage(['A', 'B']), /1\. A\n2\. B$/);
});

test('move swaps within bounds only', () => {
  assert.deepEqual(move(['a', 'b', 'c'], 0, 1), ['b', 'a', 'c']);
  assert.deepEqual(move(['a', 'b'], 0, -1), ['a', 'b']);
});
