// Tool groups mirror the backend's groupForName() in tools_provider.go: the
// chat run's enabled_tools accepts either exact tool names OR these group ids,
// and an empty set means "all tools" (the default auto-orchestration). Chips let
// the user narrow the toolset per conversation without flooding the UI with all
// ~11 individual tools.
export const TOOL_GROUPS = [
  { id: "web", label: "联网", icon: "🔎", desc: "搜索 / 抓网页 / 天气 / HTTP" },
  { id: "file", label: "文件", icon: "📄", desc: "读写你的工作区" },
  { id: "gui_agent", label: "浏览器", icon: "🌐", desc: "真实浏览器自动化" },
  { id: "util", label: "工具", icon: "🧮", desc: "时间 / 计算 / 换算" },
] as const;

export type ToolGroupId = (typeof TOOL_GROUPS)[number]["id"];

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
