// What a task may do when it runs unattended (on a schedule, a watched page or
// a webhook). Searching and reading the public web is the default; each other
// capability is opt-in, and the server enforces the choice by withholding the
// tools, not by asking nicely.
//
// A run may read private data or reach the open network, never both: with
// both, text planted in a page or an event can make the model read a file and
// send it to an attacker. Reading workspace files and running code (which can
// read them) therefore take the network away, browser included.

export type TaskCapability = "workspace_write" | "workspace_read" | "code" | "browser";

export interface TaskToolPolicy {
  allow: TaskCapability[];
}

export const TASK_CAPABILITIES: { id: TaskCapability; label: string; hint: string }[] = [
  { id: "workspace_write", label: "写入工作区文件", hint: "在任务自己的对话里创建、修改文件，生成报告和图表" },
  { id: "workspace_read", label: "读取工作区文件", hint: "读取任务对话里已有的文件；开启后不能联网" },
  { id: "code", label: "执行代码", hint: "运行 shell 命令和 Python（可以读取文件）；开启后不能联网" },
  { id: "browser", label: "操作浏览器", hint: "打开网页、点击、填写表单；需要联网" },
];

const PRIVATE: TaskCapability[] = ["workspace_read", "code"];
const NETWORK: TaskCapability[] = ["browser"];

// The server applies the same default to tasks that never chose.
export const DEFAULT_TOOL_POLICY: TaskToolPolicy = { allow: ["workspace_write"] };

export function toolPolicyOf(policy?: TaskToolPolicy | null): TaskToolPolicy {
  return { allow: [...(policy?.allow ?? DEFAULT_TOOL_POLICY.allow)] };
}

export function readsPrivate(policy: TaskToolPolicy): boolean {
  return policy.allow.some(c => PRIVATE.includes(c));
}

// toggleCapability switches one capability; turning on one side of the
// private/network divide turns the other side off.
export function toggleCapability(policy: TaskToolPolicy, id: TaskCapability, on: boolean): TaskToolPolicy {
  let rest = policy.allow.filter(c => c !== id);
  if (on && PRIVATE.includes(id)) rest = rest.filter(c => !NETWORK.includes(c));
  if (on && NETWORK.includes(id)) rest = rest.filter(c => !PRIVATE.includes(c));
  return { allow: on ? TASK_CAPABILITIES.map(c => c.id).filter(c => c === id || rest.includes(c)) : rest };
}

export function describeToolPolicy(policy: TaskToolPolicy): string {
  const base = readsPrivate(policy) ? "不联网" : "联网检索";
  const labels = TASK_CAPABILITIES.filter(c => policy.allow.includes(c.id)).map(c => c.label);
  return labels.length ? base + " + " + labels.join("、") : base;
}
