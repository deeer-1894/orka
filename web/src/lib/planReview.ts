// The steps a user approves for a paused plan, and the message that resumes
// the run with them. The server also receives the titles as approved_plan and
// holds the run to them; the message is what the model reads.

export function approvedSteps(titles: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of titles) {
    const t = raw.trim();
    if (!t || seen.has(t)) continue;
    seen.add(t);
    out.push(t);
  }
  return out;
}

export function approvalMessage(steps: string[]): string {
  return "已确认执行计划，请按以下步骤执行，并先用 update_plan 登记这些步骤：\n" + steps.map((s, i) => `${i + 1}. ${s}`).join("\n");
}

export const DECLINE_MESSAGE = "我取消了这个计划，不需要执行。请简短确认，不要调用任何工具。";

// move returns the list with item i shifted by delta (bounded).
export function move<T>(list: T[], i: number, delta: number): T[] {
  const j = i + delta;
  if (i < 0 || i >= list.length || j < 0 || j >= list.length) return list;
  const out = [...list];
  [out[i], out[j]] = [out[j], out[i]];
  return out;
}
