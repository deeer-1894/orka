// The UI speaks one language with one icon set: no emoji standing in for
// icons, no English labels left over. Weather conditions are content, not
// chrome, so WeatherCard may keep its emoji.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const src = fileURLToPath(new URL('../src/', import.meta.url));
const EMOJI_ALLOWED = new Set(['components/WeatherCard.tsx']);
// Names, formats and examples that stay as they are.
const ENGLISH_ALLOWED = /^(Orka|PDF|HTML|CSV|JSON|Excel|Markdown|Mermaid|Webhook|Cron|MCP|API Key|Base URL|GitHub|SSE|URL)$/;

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? files(path) : path.endsWith('.tsx') ? [path] : [];
  });
}

// Code without comments, so prose about emoji or English in a comment is fine.
const code = (text) => text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
const pictograph = /\p{Extended_Pictographic}/u;
const NOT_PICTURES = new Set(['©', '®', '™', '↔', '↕', '↖', '↗', '↘', '↙', '↩', '↪', '⌘', '⌨', '⏎', '▶', '◀', '↵']);

test('no emoji in UI code', () => {
  const found = [];
  for (const path of files(src)) {
    const rel = relative(src, path);
    if (EMOJI_ALLOWED.has(rel)) continue;
    code(readFileSync(path, 'utf8')).split('\n').forEach((line, i) => {
      for (const ch of line) if (pictograph.test(ch) && !NOT_PICTURES.has(ch)) found.push(`${rel}:${i + 1} ${ch}`);
    });
  }
  assert.deepEqual(found, []);
});

test('no English UI labels', () => {
  const found = [];
  const attr = /\b(title|aria-label|label|placeholder)=\{?(?:"([^"]*)"|'([^']*)'|`([^`$]*)`)/g;
  const text = />\s*([A-Za-z][A-Za-z ,.!?']+[A-Za-z.!?])\s*</g;
  for (const path of files(src)) {
    const rel = relative(src, path);
    code(readFileSync(path, 'utf8')).split('\n').forEach((line, i) => {
      for (const m of line.matchAll(attr)) {
        const value = (m[2] ?? m[3] ?? m[4]).trim();
        // A placeholder showing an example address or identifier is not a label.
        const example = /:\/\/|@|^[\w.-]+$/.test(value);
        if (/[A-Za-z]{3,}/.test(value) && !/[一-鿿]/.test(value) && !ENGLISH_ALLOWED.test(value) && !example) found.push(`${rel}:${i + 1} ${m[1]}=${value}`);
      }
      for (const m of line.matchAll(text)) {
        const value = m[1].trim();
        if (/[A-Za-z]{3,}\s+[A-Za-z]{2,}/.test(value) && !ENGLISH_ALLOWED.test(value)) found.push(`${rel}:${i + 1} ${value}`);
      }
    });
  }
  assert.deepEqual(found, []);
});
