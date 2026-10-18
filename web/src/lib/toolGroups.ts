import type { IconName } from "../components/Icon";
// Tool groups mirror the backend's groupForName() in tools_provider.go: the
// chat run's enabled_tools accepts either exact tool names OR these group ids,
// and an empty set means automatic access to all available tools. Chips let
// the user narrow the toolset per conversation without flooding the UI with all
// ~11 individual tools.
export const TOOL_GROUPS = [
  { id: "web", label: "联网", icon: "search", desc: "搜索 / 抓网页 / 天气 / HTTP" },
  { id: "file", label: "文件", icon: "file", desc: "读写你的工作区" },
  { id: "browser", label: "网页 DOM", icon: "globe", desc: "按网页 DOM 读取、点击、填表与页面脚本操作" },
  { id: "gui_agent", label: "GUI 视觉", icon: "deck", desc: "根据截图识别页面并进行视觉操作，与网页 DOM 共用本会话浏览器" },
  { id: "shell", label: "终端", icon: "keyboard", desc: "在工作区执行命令 / 脚本 / 代码" },
  { id: "util", label: "工具", icon: "calc", desc: "时间 / 计算 / 换算" },
  { id: "office", label: "办公", icon: "chart", desc: "汇率 / 时区 / 二维码 / CSV / Excel / 文档读写 / 图表 / SQL / PPT" },
  { id: "code", label: "代码", icon: "code", desc: "在沙箱里运行 Python(含 pandas/numpy)" },
] as const;

export type ToolGroupId = (typeof TOOL_GROUPS)[number]["id"];

import type { ToolInfo } from "../api";

// Display metadata for a group id, falling back for groups the static list
// doesn't name (e.g. the gateway's skill tools).
const GROUP_META: Record<string, { label: string; icon: IconName; desc: string }> = Object.fromEntries(
  TOOL_GROUPS.map((g) => [g.id, { label: g.label, icon: g.icon, desc: g.desc }]),
);
const FALLBACK_META: Record<string, { label: string; icon: IconName; desc: string }> = {
  skill: { label: "技能", icon: "sparkle", desc: "查找 / 创建 / 安装可复用技能" },
  "": { label: "其他", icon: "wrench", desc: "未分类工具" },
};
export function groupMeta(id: string): { label: string; icon: IconName; desc: string } {
  return GROUP_META[id] || FALLBACK_META[id] || { label: id, icon: "wrench", desc: "" };
}

export type CatalogGroup = { id: string; label: string; icon: IconName; desc: string; tools: ToolInfo[] };

// groupCatalog buckets the flat tool catalog into ordered, labelled groups,
// preserving TOOL_GROUPS' order and appending any unknown groups after.
export function groupCatalog(catalog: ToolInfo[]): CatalogGroup[] {
  const byGroup = new Map<string, ToolInfo[]>();
  for (const t of catalog) {
    if (!byGroup.has(t.group)) byGroup.set(t.group, []);
    byGroup.get(t.group)!.push(t);
  }
  const order = [...TOOL_GROUPS.map((g) => g.id as string), ...[...byGroup.keys()].filter((g) => !GROUP_META[g])];
  const seen = new Set<string>();
  const out: CatalogGroup[] = [];
  for (const id of order) {
    if (seen.has(id) || !byGroup.has(id)) continue;
    seen.add(id);
    const m = groupMeta(id);
    out.push({ id, ...m, tools: byGroup.get(id)!.sort((a, b) => a.name.localeCompare(b.name)) });
  }
  return out;
}

// Selection lives in a flat Set<string> of group ids AND/OR tool names (the
// backend's filterEnabled matches either). These helpers interpret + mutate it
// so a group can be toggled whole, or refined down to individual tools.
export function isToolOn(sel: Set<string>, g: CatalogGroup, name: string): boolean {
  return sel.has(g.id) || sel.has(name);
}
export function isGroupOn(sel: Set<string>, g: CatalogGroup): boolean {
  return sel.has(g.id) || g.tools.every((t) => sel.has(t.name));
}
export function isGroupPartial(sel: Set<string>, g: CatalogGroup): boolean {
  return !isGroupOn(sel, g) && g.tools.some((t) => sel.has(t.name));
}

export function toggleGroupSel(sel: Set<string>, g: CatalogGroup): Set<string> {
  const next = new Set(sel);
  const on = isGroupOn(next, g);
  for (const t of g.tools) next.delete(t.name);
  next.delete(g.id);
  if (!on) next.add(g.id); // turning on → collapse to the group id
  return next;
}

export function toggleToolSel(sel: Set<string>, g: CatalogGroup, name: string): Set<string> {
  const next = new Set(sel);
  const on = isToolOn(next, g, name);
  if (next.has(g.id)) {
    // Materialize the group into its members, then drop/keep this one.
    next.delete(g.id);
    for (const t of g.tools) next.add(t.name);
  }
  if (on) next.delete(name);
  else next.add(name);
  // Collapse back to the group id if every member ended up selected.
  if (g.tools.every((t) => next.has(t.name))) {
    for (const t of g.tools) next.delete(t.name);
    next.add(g.id);
  }
  return next;
}

const KEY = (cid: string) => `orka.tools.${cid || "_new"}`;

export function loadTools(cid: string): Set<string> {
  try {
    const raw = localStorage.getItem(KEY(cid));
    if (!raw) return new Set();
    return new Set(JSON.parse(raw) as string[]);
  } catch {
    return new Set();
  }
}

export function saveTools(cid: string, set: Set<string>) {
  try {
    if (set.size === 0) localStorage.removeItem(KEY(cid));
    else localStorage.setItem(KEY(cid), JSON.stringify([...set]));
  } catch {
    /* ignore quota / private-mode errors */
  }
}
