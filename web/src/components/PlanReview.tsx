import { useState } from "react";
import { Icon } from "./Icon";
import { approvalMessage, approvedSteps, DECLINE_MESSAGE, move } from "../lib/planReview";

// PlanReviewCard is where a run that was asked to "先确认计划" waits: the
// proposed steps, editable in place (rename, add, remove, reorder), then
// 开始执行 resumes the run held to exactly these steps.
export function PlanReviewCard({ resumeKey, proposed, active, onResume }: {
  resumeKey: string;
  proposed: string[];
  active: boolean;
  onResume: (key: string, answer: string, approvedPlan?: string[]) => void;
}) {
  const [steps, setSteps] = useState<string[]>(() => (proposed.length ? proposed : [""]));
  const [sent, setSent] = useState<"" | "approved" | "declined">("");
  const live = active && !sent;
  const ready = approvedSteps(steps);
  const set = (i: number, v: string) => setSteps(s => s.map((x, k) => (k === i ? v : x)));
  return <section aria-label="执行计划审阅" className="mb-6 ml-[42px] rounded-xl border border-accent/40 bg-accentsoft/30 p-3.5">
    <div className="mb-2 flex items-center gap-2 text-[13px] font-medium text-ink">
      <Icon name="check" size={14} className="text-accent" />
      执行计划{live ? " · 等待你确认" : sent === "approved" ? " · 已确认" : sent === "declined" ? " · 已取消" : ""}
      {live && <span className="font-normal text-faint">可修改、增删或调整顺序</span>}
    </div>
    <ol className="space-y-1.5">
      {steps.map((s, i) => <li key={i} className="flex items-center gap-1.5">
        <span className="w-5 shrink-0 text-right text-[12px] text-faint">{i + 1}</span>
        {live ? <>
          <input value={s} onChange={e => set(i, e.target.value)} aria-label={`第 ${i + 1} 步`} placeholder="描述这一步"
            className="min-w-0 flex-1 rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] text-ink outline-none focus:border-accent/50" />
          <button onClick={() => setSteps(l => move(l, i, -1))} disabled={i === 0} aria-label={`上移第 ${i + 1} 步`} className="grid h-7 w-7 place-items-center rounded-md text-faint hover:bg-surface2 hover:text-ink disabled:opacity-30"><Icon name="chevron" size={13} className="rotate-180" /></button>
          <button onClick={() => setSteps(l => move(l, i, 1))} disabled={i === steps.length - 1} aria-label={`下移第 ${i + 1} 步`} className="grid h-7 w-7 place-items-center rounded-md text-faint hover:bg-surface2 hover:text-ink disabled:opacity-30"><Icon name="chevron" size={13} /></button>
          <button onClick={() => setSteps(l => l.filter((_, k) => k !== i))} disabled={steps.length === 1} aria-label={`删除第 ${i + 1} 步`} className="grid h-7 w-7 place-items-center rounded-md text-faint hover:bg-surface2 hover:text-accent disabled:opacity-30"><Icon name="close" size={13} /></button>
        </> : <span className="text-[13px] text-muted">{s}</span>}
      </li>)}
    </ol>
    {live && <div className="mt-2.5 flex flex-wrap items-center gap-2">
      <button onClick={() => setSteps(l => [...l, ""])} className="inline-flex items-center gap-1 rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[12.5px] text-muted hover:text-ink"><Icon name="plus" size={13} />添加步骤</button>
      <span className="flex-1" />
      <button onClick={() => { setSent("declined"); onResume(resumeKey, DECLINE_MESSAGE, []); }} className="rounded-lg border border-border bg-surface px-3 py-1.5 text-[12.5px] text-muted hover:border-accent/40">取消</button>
      <button disabled={ready.length === 0} onClick={() => { setSent("approved"); onResume(resumeKey, approvalMessage(ready), ready); }}
        className="rounded-lg bg-accent px-3 py-1.5 text-[12.5px] text-white hover:opacity-90 disabled:opacity-50">开始执行</button>
    </div>}
  </section>;
}
