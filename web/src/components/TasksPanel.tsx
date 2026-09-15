import { useState } from "react";
import { api } from "../api";
import type { TaskMeta } from "../types";
import { toast } from "../lib/toast";
import { useResource, refreshResource } from "../lib/useResource";
import { Icon } from "./Icon";
import { PanelEmpty as Blank } from "./PanelEmpty";
import { fmtRunWhen as fmtWhen } from "../lib/runPresentation";
const RUN_COLOR: Record<string, string> = {
  running: "text-accent", done: "text-ok", failed: "text-accent", paused: "text-muted", start: "text-muted",
};
const INTERVALS = [
  { label: "每分钟", sec: 60 },
  { label: "每 10 分钟", sec: 600 },
  { label: "每小时", sec: 3600 },
  { label: "每天", sec: 86400 },
];

function taskPrompt(t: TaskMeta): string {
  const v = (t.variables || {}) as Record<string, unknown>;
  return String(v.prompt_template || v.prompt || v.title || "—");
}
export function TasksPanel({ onJumpToConversation }: { onJumpToConversation: (cid: string) => void }) {
  const [hookUrl, setHookUrl] = useState<Record<string, string>>({});
  const [creating, setCreating] = useState(false);
  const [prompt, setPrompt] = useState("");
  const [sec, setSec] = useState(3600);

  const tasks = useResource<TaskMeta[]>("tasks", () => api.getTasks().then((r) => r.tasks || []), { interval: 5000 }) ?? [];
  const refresh = () => refreshResource("tasks");

  const create = async () => {
    if (!prompt.trim()) return;
    try {
      await api.scheduleTask(prompt.trim(), sec, prompt.trim().slice(0, 24));
    } catch {
      toast("定时任务创建失败,请重试", "error");
      return;
    }
    setPrompt("");
    setCreating(false);
    refresh();
  };

  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[12px] text-faint">任务 · {tasks.length}</span>
        <button
          onClick={() => setCreating((o) => !o)}
          className="rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40"
        >
          {creating ? "取消" : "+ 定时任务"}
        </button>
      </div>

      {creating && (
        <div className="mb-3 space-y-2 rounded-xl border border-border bg-surface2/40 p-3">
          <textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            rows={2}
            placeholder="让 Orka 定时做什么?例如:总结今天的科技新闻"
            className="w-full resize-none rounded-lg border border-border bg-surface px-2.5 py-2 text-[13px] outline-none focus:border-accent/50"
          />
          <div className="flex items-center gap-2">
            <select
              value={sec}
              onChange={(e) => setSec(Number(e.target.value))}
              className="rounded-lg border border-border bg-surface px-2 py-1.5 text-[13px] outline-none"
            >
              {INTERVALS.map((i) => (
                <option key={i.sec} value={i.sec}>{i.label}</option>
              ))}
            </select>
            <button
              onClick={create}
              disabled={!prompt.trim()}
              className="ml-auto rounded-lg bg-accent px-3 py-1.5 text-[13px] text-white disabled:opacity-40"
            >
              创建
            </button>
          </div>
        </div>
      )}

      {tasks.length === 0 && (
        <Blank icon="clock" title="还没有定时任务" action={{ label: "+ 新建定时任务", onClick: () => setCreating(true) }}>
          把一句指令设成定时任务,Orka 会按周期自动跑(比如每天早上汇总昨日数据),也可以开 webhook 用外部事件触发。
        </Blank>
      )}
      <div className="space-y-1.5">
        {tasks.map((t) => {
          const scheduled = t.cron_status === "on";
          return (
            <div key={t.task_id} className="rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
              <div className="flex items-center gap-2">
                <span className={"text-[11px] font-medium " + (RUN_COLOR[t.run_status] || "text-muted")}>
                  ● {t.run_status}
                </span>
                {scheduled && (
                  <span className="rounded-full bg-accentsoft px-1.5 py-0.5 text-[10px] text-accent">
                    定时 · {INTERVALS.find((i) => i.sec === t.interval_sec)?.label || (t.interval_sec || 0) + "s"}
                  </span>
                )}
                <span className="ml-auto text-[10px] text-faint">{fmtWhen(t.created_at)}</span>
              </div>
              <div className="mt-1 line-clamp-2 text-[13px] text-ink">{taskPrompt(t)}</div>
              {scheduled && t.next_run_at ? (
                <div className="mt-1 text-[11px] text-faint">下次运行 {fmtWhen(t.next_run_at)}</div>
              ) : null}
              {t.last_result ? (
                <div className="mt-1 line-clamp-2 text-[11px] text-muted">↳ {t.last_result}</div>
              ) : null}
              <div className="mt-1.5 flex items-center gap-3">
                {t.conversation_id && (
                  <button
                    onClick={() => onJumpToConversation(t.conversation_id)}
                    className="text-[11px] text-accent hover:underline"
                  >
                    ↗ 打开对话
                  </button>
                )}
                {scheduled && (
                  <button
                    onClick={() => api.unscheduleTask(t.task_id).then(refresh).catch(() => toast("停用定时失败,请重试", "error"))}
                    className="text-[11px] text-faint hover:text-accent"
                  >
                    停用定时
                  </button>
                )}
                {t.webhook_token ? (
                  <button onClick={() => api.disableWebhook(t.task_id).then(refresh).catch(() => toast("关闭 webhook 失败,请重试", "error"))} className="text-[11px] text-faint hover:text-accent">关闭 webhook</button>
                ) : (
                  <button
                    onClick={() => api.enableWebhook(t.task_id).then((r) => { setHookUrl((m) => ({ ...m, [t.task_id]: location.origin + r.path })); refresh(); }).catch(() => toast("开启 webhook 失败,请重试", "error"))}
                    className="inline-flex items-center gap-1 text-[11px] text-faint hover:text-accent"
                  >
                    <Icon name="link" size={11} /> webhook
                  </button>
                )}
              </div>
              {(hookUrl[t.task_id] || t.webhook_token) && (
                <div className="mt-1 flex items-center gap-2 rounded-lg bg-surface2 px-2 py-1 text-[10px] text-muted">
                  <code className="min-w-0 flex-1 truncate">{hookUrl[t.task_id] || `${location.origin}/api/v1/controller/hook/${t.webhook_token}`}</code>
                  <button
                    onClick={() => navigator.clipboard?.writeText(hookUrl[t.task_id] || `${location.origin}/api/v1/controller/hook/${t.webhook_token}`)}
                    aria-label="复制 webhook URL" className="shrink-0 hover:text-accent"
                  >⧉</button>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
