import type { ToolPayload } from '../types';

export interface ExecutionResult {
  ok: boolean;
  exit_code?: number;
  stdout?: string;
  stderr?: string;
  timed_out?: boolean;
  canceled?: boolean;
  action?: string;
  url?: string;
  elapsed_ms?: number;
  error?: string | { code?: string; message?: string };
  file_changes?: { paths?: unknown[]; partial?: boolean };
}

// The gateway emits one JSON line. The adapter may prepend its error envelope
// or append inspection notes; stdout is data inside JSON, never a status signal.
export function executionResult(p: ToolPayload): ExecutionResult | undefined {
  if (!['shell', 'python', 'browser'].includes(p.tool)) return undefined;
  const text = (typeof p.result === 'string' ? p.result : '').trim()
    .replace(/^tool error \((?:shell|python|browser), [^\n]*?\): /, '')
    .replace(/^tool "(?:shell|python|browser)" error: /, '');
  // Support a complete JSON observation as well as the one-line adapter format.
  for (const candidate of [text, text.split('\n', 1)[0]]) {
    try {
      const value = JSON.parse(candidate);
      if (value && typeof value.ok === 'boolean') return value;
    } catch { /* legacy non-JSON observations have no structured receipt */ }
  }
  return undefined;
}

function nonempty(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined;
}

function receiptError(p: ToolPayload, receipt?: ExecutionResult): string | undefined {
  if (nonempty(p.error)) return p.error;
  // Admission and transport failures may occur before a structured browser
  // receipt exists. Only recognize the adapter's anchored envelope, never
  // words such as "error" inside page text or a successful process's stdout.
  if (!receipt && ['browser', 'shell', 'python'].includes(p.tool) && typeof p.result === 'string') {
    const first = p.result.trim().split('\n', 1)[0];
    if (/^tool call failed:|^tool error \((?:browser|shell|python),|^tool "(?:browser|shell|python)" error:/.test(first)) return first;
  }
  if (!receipt || receipt.ok) return undefined;
  if (receipt.timed_out) return '执行超时';
  if (receipt.canceled) return '执行已取消';
  const error = receipt.error;
  if (typeof error === 'string' && nonempty(error)) return error;
  if (error && typeof error === 'object') {
    const message = nonempty(error.message), code = nonempty(error.code);
    if (message) return code ? `${message}（${code}）` : message;
    if (code) return `浏览器操作失败（${code}）`;
  }
  if (p.tool === 'browser') return '浏览器操作失败';
  return `执行失败（退出码 ${receipt.exit_code ?? '未知'}）`;
}

const BROWSER_ACTIONS: Record<string, string> = {
  open: '打开网页', navigate: '打开网页', snapshot: '读取页面', preview: '预览网页',
  click: '点击元素', fill: '填写内容', type: '输入内容', select: '选择选项', press: '按键',
  fill_form: '填写表单', scroll: '滚动页面', wait: '等待页面', evaluate: '执行页面脚本',
  screenshot: '截取页面', download: '下载文件',
};

export interface ToolReceipt {
  error?: string;
  elapsedMs?: number;
  browser?: { label: string; detail: string };
}

// Single boundary for timeline status, browser presentation and measured time.
// Never derive execution time from message timestamps: they include model time.
export function normalizeToolReceipt(p: ToolPayload): ToolReceipt {
  const receipt = executionResult(p);
  const elapsed = p.elapsed_ms ?? receipt?.elapsed_ms;
  const normalized: ToolReceipt = {
    error: receiptError(p, receipt),
    elapsedMs: typeof elapsed === 'number' && Number.isFinite(elapsed) && elapsed >= 0 ? elapsed : undefined,
  };
  if (p.tool === 'browser') {
    const action = nonempty(receipt?.action) || nonempty(p.args?.action);
    normalized.browser = {
      label: action ? BROWSER_ACTIONS[action] || `浏览器操作（${action}）` : '浏览器操作',
      detail: nonempty(receipt?.url) || nonempty(p.args?.url) || nonempty(p.args?.path) || '',
    };
  }
  return normalized;
}

export function executionError(p: ToolPayload): string | undefined {
  return normalizeToolReceipt(p).error;
}

// A partial sum would look like the complete batch time. Hide it if any call
// lacks a measurement; zero is a valid measured duration.
export function totalToolElapsedMs(payloads: ToolPayload[]): number | undefined {
  if (!payloads.length) return undefined;
  let total = 0;
  for (const payload of payloads) {
    const elapsed = normalizeToolReceipt(payload).elapsedMs;
    if (elapsed === undefined) return undefined;
    total += elapsed;
  }
  return Number.isFinite(total) ? total : undefined;
}
