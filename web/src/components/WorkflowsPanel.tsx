import { useEffect, useState } from "react";
import { api } from "../api";
import type { Workflow } from "../types";
import { toast } from "../lib/toast";
import { Icon } from "./Icon";
import { PanelEmpty as Blank } from "./PanelEmpty";
type DraftStep = { name: string; prompt: string; depends_on: string[]; run_if: string; on_error: string };
const emptyStep = (i: number): DraftStep => ({ name: `step${i + 1}`, prompt: "", depends_on: [], run_if: "", on_error: "stop" });

export function WorkflowsPanel({ onJumpToConversation }: { onJumpToConversation: (cid: string) => void }) {
  const [flows, setFlows] = useState<Workflow[]>([]);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [steps, setSteps] = useState<DraftStep[]>([emptyStep(0)]);
  const refresh = () => api.listWorkflows().then((r) => setFlows(r.workflows || [])).catch(() => {});
  useEffect(() => { refresh(); }, []);

  const setStep = (i: number, patch: Partial<DraftStep>) => setSteps((ss) => ss.map((s, j) => (j === i ? { ...s, ...patch } : s)));
  const reset = () => { setName(""); setSteps([emptyStep(0)]); setAdding(false); };

  const save = async () => {
    const clean = steps
      .filter((s) => s.prompt.trim())
      .map((s) => ({
        name: s.name.trim() || "step",
        prompt: s.prompt.trim(),
        depends_on: s.depends_on.filter(Boolean),
        run_if: s.run_if.trim() || undefined,
        on_error: s.on_error !== "stop" ? s.on_error : undefined,
      }));
    if (!name.trim() || clean.length === 0) return;
    try {
      await api.createWorkflow(name.trim(), clean);
    } catch {
      toast("流程创建失败,请重试", "error");
      return; // keep the form so the user doesn't lose their steps
    }
    reset(); refresh();
  };
  const run = async (id: string) => {
    let r;
    try {
      r = await api.runWorkflow(id);
    } catch {
      toast("流程启动失败,请重试", "error");
      return;
    }
    if (r?.conversation_id) { toast("流程已启动", "success"); onJumpToConversation(r.conversation_id); }
    else toast("流程已启动,但未返回会话", "info");
  };

  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[12px] text-faint">工作流 · {flows.length}</span>
        <button onClick={() => (adding ? reset() : setAdding(true))} className="rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40">
          {adding ? "取消" : "+ 新建流程"}
        </button>
      </div>
      {adding && (
        <div className="mb-3 space-y-2 rounded-xl border border-border bg-surface2/40 p-3">
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="流程名称，如 每日竞品简报" className="w-full rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] outline-none focus:border-accent/50" />
          {steps.map((s, i) => {
            const priors = steps.slice(0, i).map((p) => p.name).filter(Boolean);
            return (
              <div key={i} className="space-y-1.5 rounded-lg border border-border/70 bg-surface p-2">
                <div className="flex items-center gap-1.5">
                  <span className="text-[11px] text-faint">#{i + 1}</span>
                  <input value={s.name} onChange={(e) => setStep(i, { name: e.target.value.replace(/\s+/g, "_") })} placeholder="步骤名" className="w-24 rounded border border-border bg-surface2 px-1.5 py-0.5 text-[12px] outline-none" />
                  {steps.length > 1 && <button onClick={() => setSteps((ss) => ss.filter((_, j) => j !== i))} className="ml-auto text-faint hover:text-accent" aria-label="移除步骤"><Icon name="close" size={13} /></button>}
                </div>
                <textarea value={s.prompt} onChange={(e) => setStep(i, { prompt: e.target.value })} rows={2} placeholder={'该步要做什么(可用 {{前一步名}} 引用其输出)'} className="w-full resize-none rounded border border-border bg-surface2 px-2 py-1 text-[12.5px] outline-none focus:border-accent/50" />
                {priors.length > 0 && (
                  <div className="flex flex-wrap items-center gap-1 text-[11px]">
                    <span className="text-faint">依赖:</span>
                    {priors.map((p) => {
                      const on = s.depends_on.includes(p);
                      return (
                        <button key={p} onClick={() => setStep(i, { depends_on: on ? s.depends_on.filter((d) => d !== p) : [...s.depends_on, p] })}
                          className={"rounded-full border px-1.5 py-0.5 " + (on ? "border-accent/40 bg-accentsoft text-accent" : "border-border text-faint")}>
                          {p}
                        </button>
                      );
                    })}
                    <span className="text-faint/60">空=接在上一步后</span>
                  </div>
                )}
                <div className="flex items-center gap-1.5">
                  <input value={s.run_if} onChange={(e) => setStep(i, { run_if: e.target.value })} placeholder="条件(可选)，如 step1 contains FOUND" className="min-w-0 flex-1 rounded border border-border bg-surface2 px-1.5 py-0.5 text-[11.5px] outline-none" />
                  <select value={s.on_error} onChange={(e) => setStep(i, { on_error: e.target.value })} className="rounded border border-border bg-surface2 px-1 py-0.5 text-[11.5px] text-muted outline-none" title="出错时">
                    <option value="stop">出错停止</option>
                    <option value="continue">出错继续</option>
                    <option value="retry:2">重试 2 次</option>
                    <option value="retry:3">重试 3 次</option>
                  </select>
                </div>
              </div>
            );
          })}
          <button onClick={() => setSteps((ss) => [...ss, emptyStep(ss.length)])} className="w-full rounded-lg border border-dashed border-border py-1 text-[12px] text-faint hover:border-accent/40 hover:text-accent">+ 添加步骤</button>
          <button onClick={save} disabled={!name.trim() || !steps.some((s) => s.prompt.trim())} className="w-full rounded-lg bg-accent px-3 py-1.5 text-[13px] text-white disabled:opacity-40">保存流程</button>
        </div>
      )}
      {flows.length === 0 && !adding && (
        <Blank icon="share" title="还没有工作流" action={{ label: "+ 新建流程", onClick: () => setAdding(true) }}>
          把一件多步骤的事拆成几步并保存下来:可以设步骤依赖、条件跳过和失败重试,Orka 按 DAG 执行,之后一键复跑。
        </Blank>
      )}
      <div className="space-y-1.5">
        {flows.map((wf) => (
          <div key={wf.workflow_id} className="rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
            <div className="flex items-center gap-2">
              <span className="text-[15px]">🧩</span>
              <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{wf.name}</span>
              <span className="text-[11px] text-faint">{wf.steps.length} 步</span>
            </div>
            <ol className="mt-1 ml-5 list-decimal text-[11px] text-muted">
              {wf.steps.slice(0, 5).map((s, i) => (
                <li key={i} className="truncate">
                  <span className="text-ink/80">{s.name}</span>
                  {(s.depends_on?.length ?? 0) > 0 && <span className="text-faint"> ←{s.depends_on!.join(",")}</span>}
                  {s.run_if && <span className="text-accent"> ?{s.run_if}</span>}
                  <span className="text-faint"> · {s.prompt}</span>
                </li>
              ))}
            </ol>
            <div className="mt-1.5 flex items-center gap-3 text-[11px]">
              <button onClick={() => run(wf.workflow_id)} className="inline-flex items-center gap-1 text-accent hover:underline"><Icon name="play" size={11} /> 运行</button>
              <button onClick={() => api.deleteWorkflow(wf.workflow_id).then(refresh)} className="text-faint hover:text-accent">删除</button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

// parseHeaders turns "Key: Value" lines into a header object.
