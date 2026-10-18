import type { Message } from '../types';

// Persisted human_input + request_id is the existing steering acceptance event.
// Ordinary prompts share human_input; neither it nor a local echo proves steering.
export function steeringReceipt(message: Message): string | undefined {
  const payload = message.payload as { request_id?: unknown } | undefined;
  if (message.type !== 'chat' || message.role !== 'user' || message.action !== 'human_input' ||
      !message.meta?.run_id || typeof payload?.request_id !== 'string' || !payload.request_id.trim()) return undefined;
  return '已加入当前任务';
}
