// What a task may do when it runs unattended (on a schedule, a watched page or
// a webhook). Reading is always allowed; each side effect is opt-in, and the
// server enforces the choice by withholding the tools, not by asking nicely.

export type TaskCapability = "workspace_write" | "code" | "browser";

export interface TaskToolPolicy {
  allow: TaskCapability[];
}

export const TASK_CAPABILITIES: { id: TaskCapability; label: string; hint: string }[] = [
  { id: "workspace_write", label: "写入工作区文件", hint: "在任务自己的对话里创建、修改文件，生成报告和图表" },
  { id: "code", label: "执行代码", hint: "运行 shell 命令和 Python" },
  { id: "browser", label: "操作浏览器", hint: "打开网页、点击、填写表单" },
];

// The server applies the same default to tasks that never chose.
export const DEFAULT_TOOL_POLICY: TaskToolPolicy = { allow: ["workspace_write"] };

export function toolPolicyOf(policy?: TaskToolPolicy | null): TaskToolPolicy {
  return { allow: [...(policy?.allow ?? DEFAULT_TOOL_POLICY.allow)] };
}

export function toggleCapability(policy: TaskToolPolicy, id: TaskCapability, on: boolean): TaskToolPolicy {
  const rest = policy.allow.filter(c => c !== id);
  return { allow: on ? TASK_CAPABILITIES.map(c => c.id).filter(c => c === id || rest.includes(c)) : rest };
}

export function describeToolPolicy(policy: TaskToolPolicy): string {
  const labels = TASK_CAPABILITIES.filter(c => policy.allow.includes(c.id)).map(c => c.label);
  return labels.length ? "只读 + " + labels.join("、") : "只读";
}
