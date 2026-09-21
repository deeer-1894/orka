import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
const require = createRequire(import.meta.url);
const { runStatistics } = require(join(process.env.ORKA_TEST_BUILD, 'lib/runStatistics.js'));

test('interruptions count as incomplete runs; live and approval time do not distort ended-run duration', () => {
  const stats = runStatistics([
    { status: 'done', duration_ms: 2000 }, { status: 'partial', duration_ms: 4000 },
    { status: 'failed', duration_ms: 6000 }, { status: 'interrupted', duration_ms: 8000 },
    { status: 'paused', duration_ms: 900000 }, { status: 'running', duration_ms: 900000 },
  ]);
  assert.equal(stats.completionRate, 25);
  assert.equal(stats.ended, 4);
  assert.equal(stats.averageSeconds, 5);
  assert.equal(stats.paused, 1);
});
test('no outcome or no valid duration displays absence, not a fabricated zero', () => {
  assert.equal(runStatistics([{ status: 'paused', duration_ms: 4000 }]).completionRate, undefined);
  const stats = runStatistics([{ status: 'done', duration_ms: NaN }, { status: 'interrupted', duration_ms: -1 }]);
  assert.equal(stats.completionRate, 50);
  assert.equal(stats.averageSeconds, undefined);
});
