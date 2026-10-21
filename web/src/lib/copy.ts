// The product's shared vocabulary: names of places and states that several
// screens show. One definition keeps them consistent (a run is 部分完成 on
// every screen, never 部分成功 on one), and is the list an English interface
// would translate first. Wording used by a single component stays with it.

export const COPY = {
  newChat: "新对话",
  conversation: "对话",
  project: "项目",
  tasks: "定时任务",
  workbench: "工作台",
  settings: "设置",
  toggleSidebar: "切换侧栏",
  sidebarHint: "显示 / 隐藏侧栏",
  untitled: "未命名对话",
} as const;

// RUN_STATUS names a run's state the same way everywhere it is shown.
export const RUN_STATUS: Record<string, string> = {
  running: "运行中",
  done: "已完成",
  partial: "部分完成",
  failed: "失败",
  paused: "等待确认",
  interrupted: "已中断",
  stopped: "已停止",
};

export const runStatusLabel = (status: string) => RUN_STATUS[status] || status;
