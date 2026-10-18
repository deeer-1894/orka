export const PREVIEW_MAX_BYTES = 100_000;
export const CSV_MAX_ROWS = 200;
export const CSV_MAX_COLUMNS = 30;

// Stream only the preview budget, even when the server ignores Range headers.
export async function readBoundedText(response: Response, maxBytes = PREVIEW_MAX_BYTES): Promise<{ text: string; truncated: boolean }> {
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  if (!response.body) return { text: '', truncated: false };
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let text = '', bytes = 0, truncated = false;
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      const remaining = Math.max(0, maxBytes - bytes);
      text += decoder.decode(value.subarray(0, remaining), { stream: true });
      bytes += Math.min(value.length, remaining);
      if (value.length > remaining) { truncated = true; break; }
    }
    // Omit an incomplete UTF-8 character at the byte boundary.
    if (!truncated) text += decoder.decode();
  } finally {
    if (truncated) await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
  return { text, truncated };
}

// Quoting is stateful across lines. Store only bounded rows/columns; callers
// bound incoming bytes before parsing. First record is the table header.
export function parseDelimited(text: string, delimiter = ',') {
  const rows: string[][] = [];
  let row: string[] = [], field = '', quoted = false, afterQuote = false;
  let truncated = false, incomplete = false, column = 0;
  const pushField = () => {
    if (column < CSV_MAX_COLUMNS) row.push(field);
    else truncated = true;
    column++; field = ''; afterQuote = false;
  };
  let i = text.charCodeAt(0) === 0xfeff ? 1 : 0;
  const start = i;
  for (; i < text.length; i++) {
    const c = text[i];
    if (quoted) {
      if (c === '"') {
        if (text[i + 1] === '"') { if (column < CSV_MAX_COLUMNS) field += '"'; i++; }
        else { quoted = false; afterQuote = true; }
      } else if (column < CSV_MAX_COLUMNS) field += c;
    } else if (c === '"' && field === '' && !afterQuote) {
      quoted = true;
    } else if (c === delimiter) {
      pushField();
    } else if (c === '\n' || c === '\r') {
      pushField(); rows.push(row); row = []; column = 0;
      if (c === '\r' && text[i + 1] === '\n') i++;
      if (rows.length === CSV_MAX_ROWS + 1) { truncated ||= i + 1 < text.length; return { rows, truncated, incomplete }; }
    } else {
      if (afterQuote && c !== ' ' && c !== '\t') incomplete = true;
      if (column < CSV_MAX_COLUMNS) field += c;
    }
  }
  incomplete ||= quoted;
  if (i > start && (field !== '' || row.length > 0 || afterQuote || quoted || !/[\r\n]$/.test(text))) { pushField(); rows.push(row); }
  return { rows, truncated, incomplete };
}

const LANGUAGES: Record<string, string> = {
  js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'javascript',
  ts: 'typescript', tsx: 'typescript', py: 'python', go: 'go', rs: 'rust',
  java: 'java', c: 'c', h: 'c', cpp: 'cpp', hpp: 'cpp', cc: 'cpp',
  sh: 'shell', bash: 'shell', zsh: 'shell', sql: 'sql', json: 'json', ndjson: 'json',
  yaml: 'yaml', yml: 'yaml', toml: 'toml', ini: 'ini', conf: 'ini',
  html: 'html', htm: 'html', xml: 'xml', css: 'css', rb: 'ruby', php: 'php',
};
export function languageForFile(name: string): string | undefined {
  return LANGUAGES[name.split('.').pop()?.toLowerCase() || ''];
}
export type CodeToken = { text: string; kind?: 'comment' | 'string' | 'number' | 'keyword' | 'tag' };
const KEYWORDS = new Set(('const let var function return async await import from export default class extends new if else for while do switch case break continue try catch finally throw typeof instanceof in of void null undefined true false this super public private protected static interface type enum implements package func defer go chan range struct map select make fn mut pub impl use mod match self trait def lambda with as pass None True False and or not is elif except raise yield print del global nonlocal end module require puts then fi done local echo select from where join left right inner outer on group by order having limit insert into values update set delete create table drop alter distinct union all int string bool boolean float double char auto include namespace using constexpr template').split(' '));

// Tokens render as React text, never HTML. Unknown languages stay plain text.
// This best-effort lexer also bounds the number of rendered DOM nodes.
export function highlightCode(source: string, language?: string): CodeToken[] {
  if (!language) return [{ text: source }];
  const hash = ['python', 'shell', 'ruby', 'yaml', 'toml', 'ini'].includes(language);
  const slash = ['javascript', 'typescript', 'go', 'rust', 'java', 'c', 'cpp', 'php', 'css'].includes(language);
  const parts = [
    ...(language === 'html' || language === 'xml' ? ['<!--[\\s\\S]*?(?:-->|$)'] : []),
    ...(slash ? ['/\\*[\\s\\S]*?(?:\\*/|$)', '//[^\\r\\n]*'] : []),
    ...(hash ? ['#[^\\r\\n]*'] : []),
    ...(language === 'sql' ? ['--[^\\r\\n]*', '/\\*[\\s\\S]*?(?:\\*/|$)'] : []),
    ...(language === 'python' ? ['"""[\\s\\S]*?(?:"""|$)', '\\x27{3}[\\s\\S]*?(?:\\x27{3}|$)'] : []),
    '"(?:\\\\[\\s\\S]|[^"\\\\])*?(?:"|$)', "'(?:\\\\[\\s\\S]|[^'\\\\])*?(?:'|$)", '`(?:\\\\[\\s\\S]|[^`\\\\])*?(?:`|$)',
    '\\b(?:0[xX][\\da-fA-F]+|\\d+(?:\\.\\d+)?(?:[eE][+-]?\\d+)?)\\b',
    ...(language === 'html' || language === 'xml' ? ['</?[a-zA-Z][\\w:-]*'] : []),
    '\\b[A-Za-z_$][\\w$]*\\b',
  ];
  const regex = new RegExp(parts.join('|'), 'g');
  const tokens: CodeToken[] = [];
  let end = 0;
  for (const match of source.matchAll(regex)) {
    if (tokens.length >= 12_000) break;
    const at = match.index!, text = match[0];
    if (at > end) tokens.push({ text: source.slice(end, at) });
    let kind: CodeToken['kind'];
    if (text.startsWith('<!--') || (slash && /^\/[/\*]/.test(text)) || (hash && text.startsWith('#')) || (language === 'sql' && /^(--|\/\*)/.test(text))) kind = 'comment';
    else if (/^["'`]/.test(text)) kind = 'string';
    else if (/^\d/.test(text)) kind = 'number';
    else if (text.startsWith('<')) kind = 'tag';
    else if (KEYWORDS.has(language === 'sql' ? text.toLowerCase() : text)) kind = 'keyword';
    tokens.push({ text, ...(kind ? { kind } : {}) });
    end = at + text.length;
  }
  if (end < source.length) tokens.push({ text: source.slice(end) });
  return tokens;
}
