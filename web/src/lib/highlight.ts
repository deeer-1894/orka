// highlight.ts — syntax highlighting for workspace files opened in the preview.
//
// The agent's deliverables are mostly source files, and they were rendered as
// one flat block of ink: a 300-line Python file was as readable as a log dump.
//
// Two choices worth stating:
//
//  1. The library tokenizes, we colour. A stock highlight.js theme is tuned for
//     a white or slate editor and looks wrong against this app's warm paper
//     palette. hljs-core emits only class names (`hljs-keyword`, `hljs-string`),
//     so index.css maps those onto the existing --color-* tokens and highlighting
//     follows the light/dark toggle for free.
//  2. Nothing is loaded until a code file is actually opened. The core is ~8KB
//     and each grammar is its own dynamic import, so vite splits them into
//     separate chunks and a user who only ever opens .md pays nothing. This
//     matters here: the bundle already carries mermaid and katex.

import type { HLJSApi } from "highlight.js";

// Extension → grammar. Only what actually turns up in a workspace; an unlisted
// extension renders as plain text rather than being guessed at, since a wrong
// grammar colours worse than no grammar.
const grammars: Record<string, () => Promise<{ default: unknown }>> = {
  go: () => import("highlight.js/lib/languages/go"),
  python: () => import("highlight.js/lib/languages/python"),
  javascript: () => import("highlight.js/lib/languages/javascript"),
  typescript: () => import("highlight.js/lib/languages/typescript"),
  json: () => import("highlight.js/lib/languages/json"),
  yaml: () => import("highlight.js/lib/languages/yaml"),
  bash: () => import("highlight.js/lib/languages/bash"),
  sql: () => import("highlight.js/lib/languages/sql"),
  xml: () => import("highlight.js/lib/languages/xml"),
  css: () => import("highlight.js/lib/languages/css"),
  rust: () => import("highlight.js/lib/languages/rust"),
  java: () => import("highlight.js/lib/languages/java"),
  cpp: () => import("highlight.js/lib/languages/cpp"),
  c: () => import("highlight.js/lib/languages/c"),
  ini: () => import("highlight.js/lib/languages/ini"),
};

const byExt: Record<string, keyof typeof grammars> = {
  go: "go",
  py: "python", pyw: "python",
  js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript",
  ts: "typescript", tsx: "typescript",
  json: "json",
  yaml: "yaml", yml: "yaml",
  sh: "bash", bash: "bash", zsh: "bash",
  sql: "sql",
  html: "xml", htm: "xml", xml: "xml", svg: "xml",
  css: "css",
  rs: "rust",
  java: "java",
  cpp: "cpp", cc: "cpp", cxx: "cpp", hpp: "cpp",
  c: "c", h: "c",
  toml: "ini", ini: "ini", conf: "ini",
};

// languageFor reports the grammar to use for a filename, or "" for a file that
// should stay plain (.txt, .csv, .log, .md — markdown has its own renderer).
export function languageFor(name: string): string {
  const ext = name.slice(name.lastIndexOf(".") + 1).toLowerCase();
  return byExt[ext] ?? "";
}

// highlightLimit is the size past which highlighting is skipped. hljs is
// synchronous and blocks the frame; a preview that appears instantly as plain
// text beats one that hangs for a moment and then appears coloured. The preview
// already truncates reads at 100k, so this only bites on a pathological file.
const highlightLimit = 120_000;

let api: HLJSApi | null = null;
const loaded = new Set<string>();

// highlight returns HTML for `code`, or null when the file should render as
// plain text (unknown extension, oversized, or a grammar that failed to load —
// a missing chunk must degrade to readable text, never to an empty preview).
//
// The returned HTML is safe to inject: hljs escapes the source text it wraps,
// so the only markup in the result is the spans it generates itself.
export async function highlight(name: string, code: string): Promise<string | null> {
  const lang = languageFor(name);
  if (!lang || code.length > highlightLimit) return null;
  try {
    if (!api) api = (await import("highlight.js/lib/core")).default;
    if (!loaded.has(lang)) {
      const mod = await grammars[lang]();
      api.registerLanguage(lang, mod.default as never);
      loaded.add(lang);
    }
    return api.highlight(code, { language: lang, ignoreIllegals: true }).value;
  } catch {
    return null;
  }
}
