import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
const require = createRequire(import.meta.url);
const t = require(join(process.env.ORKA_TEST_BUILD, 'lib/taskTrigger.js'));
const now = new Date(2026, 9, 21, 10, 0);

test('schedule form compiles to cron and back for every preset', () => {
  const base = t.defaultScheduleForm(now);
  for (const [form, cron] of [
    [{ ...base, frequency: 'daily', time: '20:18' }, '18 20 * * *'],
    [{ ...base, frequency: 'weekdays', time: '09:05' }, '5 9 * * 1-5'],
    [{ ...base, frequency: 'weekly', time: '02:16', weekdays: [4, 2] }, '16 2 * * 2,4'],
    [{ ...base, frequency: 'monthly', time: '08:00', monthDay: 1 }, '0 8 1 * *'],
    [{ ...base, frequency: 'hourly', minute: 15 }, '15 * * * *'],
  ]) {
    const trigger = t.scheduleTrigger(form, 'Asia/Shanghai');
    assert.equal(trigger.cron, cron);
    assert.equal(trigger.timezone, 'Asia/Shanghai');
    const back = t.scheduleForm(trigger, now);
    assert.equal(back.frequency, form.frequency);
    assert.equal(t.scheduleTrigger(back, 'Asia/Shanghai').cron, cron);
  }
  assert.equal(t.scheduleForm({ kind: 'schedule', cron: '*/10 9-17 * * 1-5' }, now).frequency, 'custom');
});

test('one-off schedules carry an absolute time and no cron', () => {
  const trigger = t.scheduleTrigger({ ...t.defaultScheduleForm(now), frequency: 'once', onceAt: '2026-10-22T09:30' });
  assert.equal(trigger.cron, undefined);
  assert.equal(trigger.at, new Date('2026-10-22T09:30').getTime());
  assert.equal(t.describeTrigger(trigger, 0, now), '仅一次 · 明天 9:30');
});

test('invalid forms explain what to fix', () => {
  const base = t.defaultScheduleForm(now);
  assert.throws(() => t.scheduleTrigger({ ...base, frequency: 'weekly', weekdays: [] }), /至少选择一天/);
  assert.throws(() => t.scheduleTrigger({ ...base, frequency: 'custom', cron: '0 9 *' }), /5 段/);
});

test('cards describe every trigger kind in one line', () => {
  assert.equal(t.describeTrigger({ kind: 'schedule', cron: '18 20 * * 1-5' }, 0, now), '工作日 20:18');
  assert.equal(t.describeTrigger({ kind: 'schedule', cron: '16 2 * * 2' }, 0, now), '每星期二 2:16');
  assert.equal(t.describeTrigger({ kind: 'condition', watch_url: 'https://shop.example.com/item', contains: '有货', check_every_sec: 1800 }), 'shop.example.com 出现“有货”时 · 每 30 分钟检查');
  assert.equal(t.describeTrigger({ kind: 'condition', watch_url: 'https://example.com/a', check_every_sec: 3600 }), 'example.com 内容变化时 · 每小时检查');
  assert.equal(t.describeTrigger({ kind: 'event', event: 'pull_request.closed' }), '由 pull_request.closed 触发');
  assert.equal(t.describeTrigger({ kind: 'event' }), '由 Webhook 调用触发');
  assert.equal(t.describeTrigger(null, 3600), '每小时');
  assert.equal(t.describeTrigger({ kind: 'condition', watch_url: 'https://', check_every_sec: 3600 }), '网页 内容变化时 · 每小时检查');
});
