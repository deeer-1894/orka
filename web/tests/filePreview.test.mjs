import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
const require = createRequire(import.meta.url);
const preview = () => require(join(process.env.ORKA_TEST_BUILD, 'lib/filePreview.js'));

test('CSV preserves quoted commas, escaped quotes, multiline fields, CRLF, BOM and empty trailing fields', () => {
  const { parseDelimited } = preview();
  assert.deepEqual(parseDelimited('\uFEFFname,note,last\r\n"a,b","line 1\r\nline ""2""",\r\n').rows,
    [['name', 'note', 'last'], ['a,b', 'line 1\r\nline "2"', '']]);
  assert.deepEqual(parseDelimited('a\tb\n"x\ty"\tz', '\t').rows, [['a', 'b'], ['x\ty', 'z']]);
  assert.deepEqual(parseDelimited('').rows, []);
  assert.deepEqual(parseDelimited('a,b\r1,2\r').rows, [['a','b'], ['1','2']]);
  assert.deepEqual(parseDelimited('a,b\n\n1,2,').rows, [['a','b'], [''], ['1','2','']]);
});

test('CSV bounds data rows and columns and reports incomplete quoted fields', () => {
  const { parseDelimited, CSV_MAX_ROWS, CSV_MAX_COLUMNS } = preview();
  const result = parseDelimited(Array.from({ length: CSV_MAX_ROWS + 2 }, (_, i) => `${i},x`).join('\n'));
  assert.equal(result.rows.length, CSV_MAX_ROWS + 1); // header plus data rows
  assert.equal(result.truncated, true);
  assert.equal(parseDelimited(Array.from({ length: CSV_MAX_ROWS + 1 }, () => 'x').join('\n')).truncated, false);
  const wide = parseDelimited(Array.from({ length: CSV_MAX_COLUMNS + 1 }, () => 'x').join(','));
  assert.equal(wide.rows[0].length, CSV_MAX_COLUMNS);
  assert.equal(wide.truncated, true);
  const incomplete = parseDelimited('a,b\n1,"unterminated\ntext');
  assert.equal(incomplete.incomplete, true);
  assert.equal(incomplete.rows[1][1], 'unterminated\ntext');
});

test('text reader cancels at the byte limit and handles a UTF-8 character split across chunks', async () => {
  const { readBoundedText } = preview();
  let cancelled = false;
  const bytes = new TextEncoder().encode('中文AB');
  const stream = new ReadableStream({ start(c) { c.enqueue(bytes.slice(0, 2)); c.enqueue(bytes.slice(2)); }, cancel() { cancelled = true; } });
  const result = await readBoundedText(new Response(stream), 6);
  assert.deepEqual(result, { text: '中文', truncated: true });
  assert.equal(cancelled, true);
  assert.deepEqual(await readBoundedText(new Response('中文'), 6), { text: '中文', truncated: false });
  await assert.rejects(readBoundedText(new Response('no', { status: 404 })), /404/);
});

test('language detection and tokenization preserve source text without injecting markup', () => {
  const { languageForFile, highlightCode } = preview();
  for (const [name, lang] of [['a.TSX', 'typescript'], ['a.py','python'], ['a.go','go'], ['a.json','json'], ['a.sql','sql'], ['a.yaml','yaml'], ['README.txt', undefined]]) assert.equal(languageForFile(name), lang);
  const source = 'const html = "<img src=x onerror=alert(1)>"; // comment\nreturn 42;';
  const tokens = highlightCode(source, 'javascript');
  assert.equal(tokens.map(t => t.text).join(''), source);
  assert.ok(tokens.some(t => t.kind === 'keyword' && t.text === 'const'));
  assert.ok(tokens.some(t => t.kind === 'string' && t.text.includes('<img')));
  assert.ok(tokens.some(t => t.kind === 'comment'));
  assert.ok(tokens.some(t => t.kind === 'number' && t.text === '42'));
  assert.deepEqual(highlightCode(source, undefined), [{ text: source }]);
});
