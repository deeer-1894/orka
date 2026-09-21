// Run with node snapshot_delta_test.js (also invoked by Go tests).
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
vm.runInThisContext('(' + fs.readFileSync(path.join(__dirname, 'scripts/observation.js'), 'utf8') + ')()');

const element = (ref, role = 'link') => ({ref, role, tag: role === 'link' ? 'a' : 'button', name: 'Story ' + ref, actions: ['click', 'press']});
const page = (text, elements = [], omitted = false) => ({ok: true, url: 'https://fixture.test/', title: 'Fixture', snapshot: {id: 'doc', ready_state: 'complete', text, elements, frames: [], pixel_content: false, omitted}});
const candidate = (slot, result, q = {}, coverage) => globalThis.__orkaObserve(result, {action: 'click', selector: '#filter', ...q}, slot, coverage);
const observe = (slot, result, q = {}, coverage) => {
  const reply = candidate(slot, result, q, coverage);
  assert.equal(globalThis.__orkaCommitObservation(slot, reply.snapshot.id), true);
  return reply;
};
const begin = (result, coverage) => {const slot = {redactions: [], nonce: result.snapshot.id}; observe(slot, structuredClone(result), {}, coverage); return slot;};

test('truncated 200-link observations produce a small delta with current controls', () => {
  const elements = [element('filter', 'checkbox'), ...Array.from({length: 199}, (_, i) => element('e' + i))];
  const first = page('Minimum: 0\nMedian: 5.05\nStory list', elements, true);
  const slot = begin(first);
  const next = structuredClone(first);
  next.snapshot.text = 'Minimum: 5\nMedian: 5.05\nStory list';
  next.snapshot.elements[0].checked = true;
  const result = observe(slot, structuredClone(next));
  assert.equal(result.snapshot.mode, 'delta');
  assert.match(result.snapshot.text, /Minimum: 5/);
  assert.equal(result.snapshot.elements[0].checked, true);
  assert.equal(result.change.omitted, true);
  assert.ok(JSON.stringify(result).length < JSON.stringify(first).length / 4);
  const unchanged = observe(slot, structuredClone(next));
  assert.equal(unchanged.snapshot.mode, 'unchanged');
});

test('mixed replacement and reorder retains the ordered replacement, including duplicates', () => {
  const slot = begin(page('A\nB\nA\nold\nC'));
  const result = observe(slot, page('B\nA\nA\nnew\nC'));
  assert.equal(result.snapshot.mode, 'delta');
  assert.deepEqual(result.change.added, ['B', 'A', 'A', 'new']);
  assert.deepEqual(result.change.removed, ['A', 'B', 'A', 'old']);
  assert.match(result.snapshot.text, /B\nA\nA\nnew/);
});

test('a long replacement is not silently clipped to the 240-character summary', () => {
  const result = observe(begin(page('old')), page('x'.repeat(500) + ' CRITICAL-END'));
  assert.match(result.snapshot.text, /CRITICAL-END/);
  assert.equal(result.change.omitted, true);
});

test('link reorder or window shift returns the full current ordered range', () => {
  for (const next of [[element('b'), element('a')], [element('b'), element('c')]]) {
    const result = observe(begin(page('Same', [element('a'), element('b')], true)), page('Same', next, true));
    assert.equal(result.snapshot.mode, 'full');
    assert.equal(result.change.observed, true);
    assert.deepEqual(result.snapshot.elements, next);
  }
});

test('traversal coverage changes cannot become unchanged', () => {
  const result = observe(begin(page('Same', [], true), {textEnd: 1}), page('Same', [], true), {}, {textEnd: 2});
  assert.equal(result.snapshot.mode, 'full');
  assert.equal(result.change.observed, true);
});

test('element-only changes and action target refs remain available', () => {
  const first = page('Same', [element('a'), element('b'), element('c', 'button')], true);
  const next = structuredClone(first);
  next.snapshot.elements[1].disabled = true;
  const result = observe(begin(first), next, {ref: 'a'});
  assert.equal(result.snapshot.mode, 'delta');
  assert.deepEqual(result.snapshot.elements.map(e => e.ref), ['a', 'b', 'c']);
  assert.equal(result.snapshot.elements[1].disabled, true);
});

test('bounded unchanged observations retain progress and the complete baseline', () => {
  const first = page('Same', [element('a'), element('b', 'button')], true);
  const slot = begin(first);
  for (let i = 1; i <= 3; i++) {
    const result = observe(slot, structuredClone(first));
    assert.equal(result.snapshot.mode, 'unchanged');
    assert.equal(result.progress.unchanged_actions, i);
    assert.deepEqual(slot.observation.snapshot, first.snapshot);
  }
  const full = observe(slot, structuredClone(first), {view: 'full'});
  assert.equal(full.snapshot.mode, 'full');
  assert.deepEqual(full.snapshot.elements, first.snapshot.elements);
});

test('new redactions mask previous text, names and removed text before diffing', () => {
  const slot = begin(page('private  marker\nold', [{...element('a'), name: 'private marker'}]));
  slot.redactions.push('private  marker');
  const result = observe(slot, page('[redacted]\nnew', [{...element('a'), name: '[redacted]'}]));
  assert.doesNotMatch(JSON.stringify(result), /private/);
  assert.match(result.snapshot.text, /new/);
});

test('explicit reads, navigation and large text changes remain bounded full snapshots', () => {
  for (const q of [{action: 'snapshot'}, {action: 'open'}, {action: 'preview'}, {view: 'full'}]) {
    assert.equal(observe(begin(page('old')), page('new'), q).snapshot.mode, 'full');
  }
  const next = page('new'); next.url += 'next';
  assert.equal(observe(begin(page('old')), next).snapshot.mode, 'full');
  const long = Array.from({length: 60}, (_, i) => 'line ' + i).join('\n');
  assert.equal(observe(begin(page('old')), page(long)).snapshot.mode, 'full');
});

test('ordered splices reconstruct insertions, deletions, duplicate and permuted lines exactly', () => {
  // Exhaust a small repeated alphabet: unlike example-only tests this catches
  // interactions between common prefixes/suffixes and duplicate line counts.
  const samples = [''];
  for (let n = 1; n <= 4; n++) {
    for (let bits = 0; bits < 2 ** n; bits++) {
      samples.push(Array.from({length: n}, (_, i) => bits & (1 << i) ? 'A' : 'B').join('\n'));
    }
  }
  for (const before of samples) for (const after of samples) {
    const result = observe(begin(page(before)), page(after));
    if (before === after) {
      assert.equal(result.snapshot.mode, 'unchanged');
      continue;
    }
    assert.equal(result.snapshot.mode, 'delta');
    const lines = result.snapshot.text.split('\n');
    const match = lines.shift().match(/^\[Text update: at line (\d+) of the previous bounded text, replace (\d+) lines with (\d+) lines; other lines unchanged\.\]$/);
    assert.ok(match, result.snapshot.text);
    assert.equal(lines.length, Number(match[3]));
    const restored = before ? before.split('\n') : [];
    restored.splice(Number(match[1]) - 1, Number(match[2]), ...lines);
    assert.equal(restored.join('\n'), after);
  }
});

test('UTF-8 change summaries cannot exceed the reply budget or clip the text splice', () => {
  const elements = Array.from({length: 100}, (_, i) => ({...element('e' + i, 'button'), name: '状态'.repeat(35)}));
  const before = Array.from({length: 24}, (_, i) => '旧'.repeat(239) + i).join('\n');
  const after = Array.from({length: 24}, (_, i) => '新'.repeat(239) + i).join('\n');
  const first = page(before, elements);
  assert.ok(Buffer.byteLength(JSON.stringify(first)) < 60000);
  const result = observe(begin(first), page(after, elements));
  assert.equal(result.snapshot.mode, 'delta');
  assert.match(result.snapshot.text, /新{239}23/);
  assert.ok(Buffer.byteLength(JSON.stringify(result)) <= 61000);
  assert.equal(result.change.omitted, true);
  assert.deepEqual(result.change.added, []);
});

test('delta annotations near the text cap fall back to an intact full view', () => {
  const after = 'x'.repeat(11999);
  const elements = [element('a')];
  const result = observe(begin(page('old', elements)), page(after, elements));
  assert.equal(result.snapshot.mode, 'full');
  assert.equal(result.snapshot.text, after);
  assert.deepEqual(result.snapshot.elements, elements);
});

test('discarded candidate cannot hide a change from the next browser call', () => {
  const slot = begin(page('Status: pending', [element('a')], true));
  // Simulate observeCurrent accepting the script reply, then timing out while
  // checking stability. Neither candidate nor its private baseline was sent.
  candidate(slot, page('Status: completed', [element('a')], true));
  assert.equal(slot.observation.snapshot.text, 'Status: pending');
  const nextCall = observe(slot, page('Status: completed', [element('a')], true), {action: 'wait'});
  assert.equal(nextCall.snapshot.mode, 'delta');
  assert.equal(nextCall.change.observed, true);
  assert.match(nextCall.snapshot.text, /Status: completed/);
  assert.deepEqual(nextCall.change.removed, ['Status: pending']);
});

test('unconfirmed first reads stay full and retries do not inflate action progress', () => {
  const slot = {redactions: [], nonce: 'doc'};
  candidate(slot, page('First'));
  const nextCall = observe(slot, page('First'), {action: 'wait'});
  assert.equal(nextCall.snapshot.mode, 'full');
  for (let i = 0; i < 5; i++) candidate(slot, page('First'));
  assert.equal(slot.progress.count, 0);
  const committed = observe(slot, page('First'));
  assert.equal(committed.progress.unchanged_actions, 1);
});

test('discarded ranges and names cannot become the baseline; new secrets still redact it', () => {
  const slot = begin(page('private marker\nOld', [element('a'), element('b')], true));
  candidate(slot, page('private marker\nIntermediate', [element('b'), element('a')], true));
  slot.redactions.push('private marker');
  const nextCall = observe(slot, page('[redacted]\nFinal', [element('a'), element('b')], true), {action: 'wait'});
  assert.equal(nextCall.snapshot.mode, 'delta');
  assert.deepEqual(nextCall.change.removed, ['Old']);
  assert.doesNotMatch(JSON.stringify(nextCall), /private marker|Intermediate/);
});

test('commit rejects another document or scope and cannot be applied twice', () => {
  const slot = begin(page('Committed'));
  slot.scope = 'run-one';
  candidate(slot, page('Candidate'));
  assert.equal(globalThis.__orkaCommitObservation(slot, 'other-document'), false);
  assert.equal(slot.observation.snapshot.text, 'Committed');
  slot.scope = 'run-two';
  assert.equal(globalThis.__orkaCommitObservation(slot, 'doc'), false);
  assert.equal(slot.observation.snapshot.text, 'Committed');
  candidate(slot, page('New scope candidate'));
  assert.equal(globalThis.__orkaCommitObservation(slot, 'doc'), true);
  assert.equal(slot.observation.snapshot.text, 'New scope candidate');
  assert.equal(slot.pendingObservation, null);
  assert.equal(globalThis.__orkaCommitObservation(slot, 'doc'), false);
});

test('legacy eager baselines are discarded once when upgrading the helper', () => {
  const slot = {redactions: [], nonce: 'doc', observation: {url: 'https://fixture.test/', title: 'Fixture', snapshot: page('Unseen').snapshot}};
  const first = observe(slot, page('Unseen'), {action: 'wait'});
  assert.equal(first.snapshot.mode, 'full');
  const next = observe(slot, page('Unseen'), {action: 'wait'});
  assert.equal(next.snapshot.mode, 'unchanged');
});
