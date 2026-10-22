// Every font size in the app is a step of the type scale defined in
// src/index.css (@theme --text-*). Hand-written pixel sizes are how the UI
// drifted into 19 sizes, several half a pixel apart; this keeps them out.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const src = fileURLToPath(new URL('../src/', import.meta.url));
const files = dir => readdirSync(dir).flatMap(name => {
  const path = join(dir, name);
  return statSync(path).isDirectory() ? files(path) : /\.(tsx?|css)$/.test(path) ? [path] : [];
});

test('font sizes come from the type scale', () => {
  const found = [];
  for (const path of files(src)) {
    const rel = relative(src, path);
    readFileSync(path, 'utf8').split('\n').forEach((line, i) => {
      if (/text-\[\d+(\.\d+)?(px|rem)\]/.test(line)) found.push(`${rel}:${i + 1} ${line.trim().slice(0, 80)}`);
      // CSS: a scale variable, an em ratio, or a fluid clamp() heading only.
      if (path.endsWith('.css') && /font-size:\s*\d+(\.\d+)?px/.test(line)) found.push(`${rel}:${i + 1} ${line.trim().slice(0, 80)}`);
    });
  }
  assert.deepEqual(found, []);
});
