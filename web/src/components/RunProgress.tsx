import { useEffect, useState } from "react";
import { Icon, fileIconName } from "./Icon";
import { elapsedLabel, type DeliverySummary, type RunProgress } from "../lib/runProgress";
import { RUN_STATUS } from "../lib/copy";

// ProgressBar stays pinned at the top of the thread while a long run works, so
// "where is it now" never scrolls away with the tool calls.
export function ProgressBar({ progress, live, autoResume, onShowPlan, onCancelAutoResume }: {
  progress: RunProgress;
  live: boolean;
  autoResume?: string;
  onShowPlan?: () => void;
  onCancelAutoResume?: () => void;
}) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!live) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [live]);
  const position = progress.current ? progress.current.index + 1 : progress.done;
  const pct = progress.total ? Math.round((progress.done / progress.total) * 100) : 0;
  return <div role="status" aria-label="任务进度" className="sticky top-0 z-20 border-b border-border bg-bg/90 px-5 py-2 backdrop-blur">
    <div className="mx-auto flex max-w-3xl flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
        <span className="min-w-0 flex-1 truncate font-medium text-ink">
          {autoResume ? "服务繁忙，等待自动继续" : progress.total ? `第 ${position}/${progress.total} 步` : "执行中"}
          {!autoResume && progress.current && <span className="font-normal text-muted"> · {progress.current.title}</span>}
        </span>
        <span className="shrink-0 text-faint">{autoResume || `已用 ${elapsedLabel(now - progress.startedAt)} · ${progress.toolCalls} 次工具调用`}</span>
        {autoResume && onCancelAutoResume && <button onClick={onCancelAutoResume} className="shrink-0 text-xs text-accent hover:underline">取消</button>}
        {!autoResume && onShowPlan && progress.total > 0 && <button onClick={onShowPlan} className="shrink-0 text-xs text-muted hover:text-accent">查看计划</button>}
      </div>
      {progress.total > 0 && <div className="h-1 overflow-hidden rounded-full bg-surface2" aria-hidden>
        <div className="h-full rounded-full bg-accent transition-all" style={{ width: pct + "%" }} />
      </div>}
      {progress.agents.length > 0 && <div className="truncate text-xs text-faint">子 Agent：{progress.agents.join("、")}</div>}
    </div>
  </div>;
}

// A failed run that still made a plan delivered part of it: say 未完成, not 失败.
const STATUS_LABEL: Record<DeliverySummary["status"], string> = { done: RUN_STATUS.done, partial: RUN_STATUS.partial, failed: "未完成", stopped: RUN_STATUS.stopped };

// DeliveryCard closes a planned run with what came of it: the files it set out
// to deliver, and every step left open with the reason the run gave.
export function DeliveryCard({ summary, onOpenFile }: { summary: DeliverySummary; onOpenFile: (name: string) => void }) {
  const complete = summary.status === "done" && summary.unfinished.length === 0;
  return <section aria-label="交付摘要" className={"mb-6 ml-[42px] rounded-xl border p-3.5 " + (complete ? "border-ok/30 bg-ok/5" : "border-accent/40 bg-accentsoft/40")}>
    <div className="flex items-center gap-2 text-sm font-medium text-ink">
      <Icon name={complete ? "check" : "alert"} size={15} className={complete ? "text-ok" : "text-accent"} />
      交付摘要 · {STATUS_LABEL[summary.status]}
      <span className="font-normal text-faint">完成 {summary.done}/{summary.total} 步</span>
    </div>
    {summary.outputs.length > 0 && <div className="mt-2 flex flex-wrap gap-1.5">
      {summary.outputs.map(f => <button key={f} onClick={() => onOpenFile(f)} className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface px-2.5 py-1 text-xs text-ink hover:border-accent/40">
        <Icon name={fileIconName(f)} size={13} className="text-muted" />{f}
      </button>)}
    </div>}
    {summary.unfinished.length > 0 && <ul className="mt-2 space-y-1 text-xs">
      {summary.unfinished.map(s => <li key={s.title} className="flex gap-1.5 text-muted">
        <span className="text-accent">未完成</span><span className="text-ink">{s.title}</span>{s.reason && <span className="text-faint">· {s.reason}</span>}
      </li>)}
    </ul>}
  </section>;
}
