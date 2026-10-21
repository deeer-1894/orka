import type { IconName } from "../components/Icon";
import type { TaskTrigger } from "./taskTrigger";

// Starting points limited to what Orka can actually do unattended: search and
// read the web, watch pages, run code, write workspace files, react to webhooks.
// 【…】 marks the part a user must fill in before saving.
export interface TaskTemplate {
  id: string;
  icon: IconName;
  title: string;
  description: string;
  prompt: string;
  trigger: TaskTrigger;
}

export const TASK_TEMPLATES: TaskTemplate[] = [
  {
    id: "daily-brief", icon: "sun", title: "每日资讯简报", description: "搜索你关注领域的最新动态，整理成要点简报。",
    prompt: "搜索【关注的领域，例如：AI 智能体、半导体】过去 24 小时的重要新闻和动态，挑出最值得关注的 5 条，每条附一句话要点和原文链接，整理成简报。",
    trigger: { kind: "schedule", cron: "0 9 * * 1-5" },
  },
  {
    id: "page-change", icon: "eye", title: "网页变化提醒", description: "页面内容一有变化，就说明改了什么。",
    prompt: "监控的页面发生了变化。打开该页面，说明这次变化的具体内容，只列出实质性改动，忽略广告和时间戳之类的噪声。",
    trigger: { kind: "condition", watch_url: "https://", check_every_sec: 3600 },
  },
  {
    id: "stock-alert", icon: "coin", title: "到货 / 降价提醒", description: "页面出现指定文字时提醒你，例如“有货”。",
    prompt: "商品页面出现了关注的文字。打开页面确认当前价格、库存和购买条件，用三句话告诉我是否值得现在下单。",
    trigger: { kind: "condition", watch_url: "https://", contains: "有货", check_every_sec: 1800 },
  },
  {
    id: "release-tracker", icon: "download", title: "开源版本跟踪", description: "检查仓库的最新 Release，总结更新与破坏性变更。",
    prompt: "检查 GitHub 仓库【owner/repo，例如 cloudwego/eino】的最新 Release。如果是新版本，总结更新要点，单独列出破坏性变更和升级注意事项；没有新版本就只回复“无新版本”。",
    trigger: { kind: "schedule", cron: "30 9 * * *" },
  },
  {
    id: "dependency-check", icon: "shield", title: "依赖安全检查", description: "扫描依赖的新版本和已披露的安全漏洞。",
    prompt: "检查【项目地址或依赖清单】中的依赖是否有新版本或已披露的安全漏洞，按风险从高到低列出，并给出升级建议。",
    trigger: { kind: "schedule", cron: "0 10 * * 2" },
  },
  {
    id: "competitor-watch", icon: "users", title: "竞品动态追踪", description: "每周汇总竞品的产品更新、发布和报道。",
    prompt: "搜索【竞品名称，用逗号分隔】过去一周的产品更新、版本发布、融资和媒体报道，按公司分组整理，最后给出三条值得我们关注的变化。",
    trigger: { kind: "schedule", cron: "0 10 * * 1" },
  },
  {
    id: "release-notes", icon: "send", title: "发布说明起草", description: "PR 合并后，自动起草面向用户的发布说明。",
    prompt: "一个 Pull Request 已关闭。如果它已合并，根据事件内容起草一段面向用户的发布说明，保存为 release-notes.md；如果未合并，只回复“未合并，无需发布说明”。",
    trigger: { kind: "event", event: "pull_request.closed" },
  },
  {
    id: "weekly-report", icon: "book", title: "周报整理", description: "汇总本周工作区里的产出，生成结构化周报。",
    prompt: "汇总本任务工作区里本周新增或修改的文件，整理成结构化周报：完成事项、进展、风险与下周计划，保存为 weekly-report.md。",
    trigger: { kind: "schedule", cron: "0 17 * * 5" },
  },
];
