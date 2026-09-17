import type { ToolPayload } from '../types';

export interface ExecutionResult {
  ok: boolean;
  exit_code?: number;
  stdout?: string;
  stderr?: string;
  timed_out?: boolean;
  canceled?: boolean;
  file_changes?: { paths?: unknown[]; partial?: boolean };
}

// The gateway emits one JSON line. The adapter may prepend its error envelope
// or append inspection notes; stdout is data inside JSON, never a status signal.
export function executionResult(p: ToolPayload): ExecutionResult | undefined {
  if (p.tool !== 'shell' && p.tool !== 'python') return undefined;
  const line = (p.result || '').replace(/^tool error \((?:shell|python), [^\n]*?\): /, '')
    .replace(/^tool "(?:shell|python)" error: /, '').split('\n', 1)[0];
  try {
    const value = JSON.parse(line);
    if (value && typeof value.ok === 'boolean') return value;
  } catch { /* legacy non-JSON observations have no process receipt */ }
  return undefined;
}

export function executionError(p: ToolPayload): string | undefined {
  if (p.error) return p.error;
  const receipt = executionResult(p);
  if (!receipt || receipt.ok) return undefined;
  if (receipt.timed_out) return '执行超时';
  if (receipt.canceled) return '执行已取消';
  return `执行失败（退出码 ${receipt.exit_code ?? '未知'}）`;
}
