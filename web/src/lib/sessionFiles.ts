import type { Message, ToolPayload } from '../types';

export interface FileContext { conversationID: string; ownerEmail: string }
const WRITERS = new Set(['file_write', 'doc_export', 'chart', 'qrcode', 'csv_to_json', 'csv_to_xlsx', 'xlsx_to_csv', 'csv_join', 'slides', 'sql_query', 'pdf_extract']);
const ERROR = /^(?:error\b|failed\b|failure\b|refused\b|denied\b|permission denied\b|access denied\b|拒绝|操作被拒绝|tool\s+[\'"][^\'"]+[\'"]\s+error\b|command (?:exited with error|timed out)|错误|失败)/i;
const OUTPUT_LINE = /^(?:generated|created|saved|written|wrote|output(?: file)?|已生成|已保存|已写入|产物)\s*(?::|：|->|→|to)?\s*(.+)$/i;

export function normalizeWorkspacePath(raw: string, ownerEmail: string): string | undefined {
  let path = raw.trim();
  if (!path || /[\\\u0000-\u001f]/.test(path) || /^[a-z][a-z0-9+.-]*:/i.test(path) || path.startsWith('//')) return undefined;
  if (path.startsWith('/')) {
    // Match the full owner boundary, never a trailing basename/suffix. Other
    // absolute paths have no known mapping to this user's file API.
    const owner = ownerEmail.replace(/[/\\]/g, '_').replaceAll('..', '_');
    const marker = `/storage/${owner}/`;
    const at = owner ? path.indexOf(marker) : -1;
    if (at < 0) return undefined;
    path = path.slice(at + marker.length);
  }
  const parts = path.split('/');
  if (parts.includes('..')) return undefined;
  path = parts.filter(p => p !== '' && p !== '.').join('/');
  return path && !raw.endsWith('/') ? path : undefined;
}

const SKIPPED_CONFIRMATIONS = new Set([
  '用户拒绝了该操作,已跳过。',
  '未收到确认结果,已跳过该操作。',
  '等待用户确认超时,已跳过该操作。',
]);

function failedToolResult(p: ToolPayload): boolean {
  const flags = p as ToolPayload & { success?: boolean };
  if (flags.success === false || p.error || typeof p.result !== 'string' || !p.result.trim()) return true;
  const text = p.result.trim();
  if (SKIPPED_CONFIRMATIONS.has(text) || ERROR.test(text)) return true;
  try {
    const result = JSON.parse(text);
    if (result && typeof result === 'object' && !Array.isArray(result)) {
      return result.success === false || result.ok === false || !!result.error ||
        ['failed', 'error', 'denied', 'refused', 'rejected'].includes(result.status) ||
        (typeof result.message === 'string' && ERROR.test(result.message));
    }
  } catch { /* plain-text tool receipts remain supported */ }
  return false;
}

function outputPaths(text: string, allowFullLine = false): string[] {
  const paths: string[] = [];
  for (const line of text.split('\n')) {
    const match = line.trim().match(OUTPUT_LINE);
    if (!match && !(allowFullLine && /^\S+\/[^/]+\.[a-z0-9]+$/i.test(line.trim()))) continue;
    // Tool output must explicitly declare the complete path, not merely print
    // an arbitrary directory listing or a bare filename.
    const path = (match ? match[1] : line).trim().replace(/^[`'"]|[`'"]$/g, '');
    if (path.includes('/') && !/\s(?:and|to)\s/.test(path)) paths.push(path);
  }
  return paths;
}

export function sessionFileCandidates(messages: Message[], context: FileContext): string[] {
  const out = new Set<string>(), planned = new Set<string>();
  const add = (raw: unknown, target = out) => {
    if (typeof raw !== 'string') return;
    const path = normalizeWorkspacePath(raw, context.ownerEmail);
    if (path) target.add(path);
  };
  for (const m of messages) {
    if (m.meta?.conversation_id !== context.conversationID) continue;
    if (m.type === 'chat' && m.role === 'user') { planned.clear(); continue; }
    if (m.type === 'plan') {
      const outputs = (m.payload as { outputs?: unknown } | undefined)?.outputs;
      if (Array.isArray(outputs)) outputs.forEach(p => add(p, planned));
    }
    if (m.type === 'tool') {
      const p = m.payload as ToolPayload | undefined;
      if (!p || failedToolResult(p)) continue;
      const args = p.args || {};
      if (p.tool === 'update_plan') {
        if (Array.isArray(args.outputs)) args.outputs.forEach(p => add(p, planned));
      } else if (WRITERS.has(p.tool)) {
        // Conversion tools' path is an INPUT; their out is the output.
        add(args.out ?? args.output ?? (p.tool === 'file_write' ? args.path ?? args.filename ?? args.file : undefined));
        outputPaths(p.result!).forEach(p => add(p));
      } else if (p.tool === 'shell' || p.tool === 'python') {
        const script = p.tool === 'python' || /^\s*(?:python(?:3(?:\.\d+)?)?|node)\s/.test(String(args.command || ''));
        outputPaths(p.result!, script).forEach(p => add(p));
      }
    } else if (m.type === 'chat' && m.role === 'assistant' && m.action !== 'reasoning') {
      for (const line of (m.content || '').split('\n')) {
        if (!/(?:已生成|已保存|已完成|交付|产物|下载|generated|created|saved|deliverable|download)/i.test(line) || /(?:考虑|可能|准备|将要|will|would|could)/i.test(line)) continue;
        for (const match of line.matchAll(/\[[^\]]*\]\((?:<([^>]+)>|([^\s)]+))\)/g)) {
          try { add(decodeURIComponent(match[1] || match[2])); } catch { /* malformed URL */ }
        }
      }
    }
  }
  planned.forEach(p => out.add(p));
  return [...out];
}

// Query only declared parent directories. No workspace-wide basename map, walk
// depth cap, or matching a missing F/file to an existing B/file.
export async function existingSessionFiles(candidates: string[], list: (dir: string) => Promise<{ name: string; dir: boolean }[]>): Promise<string[]> {
  const parents = new Map<string, Set<string>>();
  for (const path of candidates) {
    const slash = path.lastIndexOf('/');
    const dir = slash < 0 ? '.' : path.slice(0, slash);
    if (!parents.has(dir)) parents.set(dir, new Set());
  }
  const entries = [...parents.keys()];
  // Bound concurrent requests even for a plan with many output directories.
  await Promise.all(Array.from({ length: Math.min(4, entries.length) }, async () => {
    for (;;) {
      const dir = entries.shift();
      if (dir === undefined) return;
      try {
        const found = await list(dir);
        for (const item of found) if (!item.dir && item.name && !/[\\/]/.test(item.name) && item.name !== '..') parents.get(dir)!.add(item.name);
      } catch { /* missing directory/file is not a produced artifact */ }
    }
  }));
  return candidates.filter(path => {
    const slash = path.lastIndexOf('/');
    return parents.get(slash < 0 ? '.' : path.slice(0, slash))?.has(path.slice(slash + 1));
  });
}
