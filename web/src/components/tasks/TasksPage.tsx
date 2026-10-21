import { useEffect, useMemo, useState } from "react";
import { api } from "../../api";
import type { RunRecord, TaskMeta } from "../../types";
import { describeTrigger, formatDateTime, type TriggerKind } from "../../lib/taskTrigger";
import { TASK_TEMPLATES, type TaskTemplate } from "../../lib/taskTemplates";
import { refreshResource, useResource } from "../../lib/useResource";
import { confirmDialog } from "../../lib/confirm";
import { toast } from "../../lib/toast";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Icon } from "../Icon";
import { KIND_ICON, TaskEditor, draftFromTask, type TaskDraft } from "./TaskEditor";
import { Switch } from "../ui/switch";

type Tab = "mine" | "templates";
type KindFilter = "all" | TriggerKind;
type StatusFilter = "all" | "on" | "off";
type Sort = "next" | "created" | "title";

const SORTS: { id: Sort; label: string }[] = [{ id: "next", label: "下次运行" }, { id: "created", label: "最近创建" }, { id: "title", label: "名称" }];
const KIND_FILTERS: { id: KindFilter; label: string }[] = [{ id: "all", label: "全部触发方式" }, { id: "schedule", label: "时间" }, { id: "condition", label: "条件" }, { id: "event", label: "事件" }];
const STATUS_FILTERS: { id: StatusFilter; label: string }[] = [{ id: "all", label: "全部状态" }, { id: "on", label: "已启用" }, { id: "off", label: "已停用" }];

const titleOf = (t: TaskMeta) => String(t.variables?.title || t.variables?.prompt_template || "未命名任务");
const promptOf = (t: TaskMeta) => String(t.variables?.prompt_template || t.variables?.prompt || "");
const kindOf = (t: TaskMeta): TriggerKind => t.trigger?.kind || "schedule";

export function blankDraft(prompt = ""): TaskDraft {
  return { title: "", prompt, trigger: { kind: "schedule", cron: "0 9 * * 1-5" }, enabled: true, retry_count: 0 };
}
const draftFromTemplate = (t: TaskTemplate): TaskDraft => ({ title: t.title, prompt: t.prompt, trigger: { ...t.trigger }, enabled: true, retry_count: 0 });

// TasksPage is the full-page home of triggered tasks: the user's tasks with
// search, filters and sort, and templates to start from.
export function TasksPage({ onOpenConversation, onToggleSidebar, initialDraft, onDraftOpened }: {
  onOpenConversation: (conversationID: string) => void;
  onToggleSidebar: () => void;
  initialDraft?: TaskDraft | null;
  onDraftOpened?: () => void;
}) {
  const tasks = useResource<TaskMeta[]>("tasks", () => api.getTasks().then(r => r.tasks || []), { interval: 15000 });
  const [tab, setTab] = useState<Tab>("mine");
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState<KindFilter>("all");
  const [status, setStatus] = useState<StatusFilter>("all");
  const [sort, setSort] = useState<Sort>("next");
  const [editing, setEditing] = useState<TaskDraft | null>(null);
  const [pending, setPending] = useState<Record<string, boolean>>({});
  useEffect(() => { if (initialDraft) { setEditing(initialDraft); onDraftOpened?.(); } }, [initialDraft, onDraftOpened]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = (tasks || []).filter(t =>
      (kind === "all" || kindOf(t) === kind) &&
      (status === "all" || (status === "on") === (t.cron_status === "on")) &&
      (!q || titleOf(t).toLowerCase().includes(q) || promptOf(t).toLowerCase().includes(q)));
    const nextKey = (t: TaskMeta) => t.cron_status === "on" && t.next_run_at ? t.next_run_at : Number.MAX_SAFE_INTEGER;
    return list.sort((a, b) => sort === "title" ? titleOf(a).localeCompare(titleOf(b), "zh")
      : sort === "created" ? b.created_at - a.created_at : nextKey(a) - nextKey(b));
  }, [tasks, query, kind, status, sort]);

  const busy = (id: string, v: boolean) => setPending(p => ({ ...p, [id]: v }));
  const toggle = async (t: TaskMeta, enabled: boolean) => {
    busy(t.task_id, true);
    try { await api.toggleTask(t.task_id, enabled); refreshResource("tasks"); toast(enabled ? "已启用" : "已停用", "success"); }
    catch { /* api already reported */ } finally { busy(t.task_id, false); }
  };
  const runNow = async (t: TaskMeta) => {
    try { await api.runTaskNow(t.task_id); toast("已开始运行，结束后会通知你", "success"); refreshResource("tasks"); refreshResource(`runs:task:${t.task_id}`); } catch { /* reported */ }
  };
  const remove = async (t: TaskMeta) => {
    if (!await confirmDialog({ title: `删除“${titleOf(t)}”？`, body: "任务的对话和已产出的文件会保留。", confirmText: "删除", danger: true })) return;
    try { await api.deleteTask(t.task_id); refreshResource("tasks"); toast("已删除", "success"); } catch { /* reported */ }
  };

  const filtered = kind !== "all" || status !== "all" || query.trim() !== "";
  return <div className="flex h-full min-h-0 flex-col">
    <header className="flex h-14 shrink-0 items-center px-2 sm:px-4">
      <button onClick={onToggleSidebar} className="grid h-8 w-8 place-items-center rounded-lg text-muted hover:bg-surface2" aria-label="切换侧栏">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M3 6h18M3 12h18M3 18h18" stroke="currentColor" strokeWidth="2" strokeLinecap="round" /></svg>
      </button>
    </header>
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-5xl px-5 pb-16 sm:px-10">
        <h1 className="font-serif text-[28px] text-ink">定时任务</h1>
        <div className="mt-6 flex flex-wrap items-center gap-2">
          <div role="tablist" aria-label="定时任务视图" className="flex items-center gap-1">
            {(["mine", "templates"] as Tab[]).map(id => <button key={id} role="tab" aria-selected={tab === id} onClick={() => setTab(id)}
              className={"rounded-lg px-3 py-1.5 text-[14px] transition " + (tab === id ? "bg-surface2 text-ink" : "text-faint hover:text-muted")}>
              {id === "mine" ? `我的${tasks?.length ? ` · ${tasks.length}` : ""}` : "模板"}</button>)}
          </div>
          <div className="ml-auto flex items-center gap-1">
            {tab === "mine" && !!tasks?.length && <>
              <label className="relative">
                <span className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-faint"><Icon name="search" size={14} /></span>
                <input value={query} onChange={e => setQuery(e.target.value)} placeholder="搜索任务" aria-label="搜索任务"
                  className="h-8 w-36 rounded-lg border border-border bg-surface pl-8 pr-2 text-[13px] text-ink outline-none transition focus:w-48 focus:border-accent/60 sm:w-44 sm:focus:w-56" />
              </label>
              <DropdownMenu>
                <DropdownMenuTrigger asChild><Button variant="ghost" size="icon" aria-label="筛选" className={filtered && (kind !== "all" || status !== "all") ? "text-accent" : ""}><Icon name="filter" size={15} /></Button></DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuLabel>触发方式</DropdownMenuLabel>
                  {KIND_FILTERS.map(f => <DropdownMenuItem key={f.id} onSelect={() => setKind(f.id)}>{kind === f.id ? <Icon name="check" size={13} /> : <span className="w-[13px]" />}{f.label}</DropdownMenuItem>)}
                  <DropdownMenuSeparator />
                  <DropdownMenuLabel>状态</DropdownMenuLabel>
                  {STATUS_FILTERS.map(f => <DropdownMenuItem key={f.id} onSelect={() => setStatus(f.id)}>{status === f.id ? <Icon name="check" size={13} /> : <span className="w-[13px]" />}{f.label}</DropdownMenuItem>)}
                </DropdownMenuContent>
              </DropdownMenu>
              <DropdownMenu>
                <DropdownMenuTrigger asChild><Button variant="ghost" size="icon" aria-label="排序"><Icon name="sort" size={15} /></Button></DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuLabel>排序</DropdownMenuLabel>
                  {SORTS.map(s => <DropdownMenuItem key={s.id} onSelect={() => setSort(s.id)}>{sort === s.id ? <Icon name="check" size={13} /> : <span className="w-[13px]" />}{s.label}</DropdownMenuItem>)}
                </DropdownMenuContent>
              </DropdownMenu>
            </>}
            <DropdownMenu>
              <DropdownMenuTrigger asChild><Button size="sm"><Icon name="plus" size={14} />新建任务</Button></DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-52">
                <DropdownMenuItem onSelect={() => setEditing(blankDraft())}><Icon name="rename" size={14} />空白任务</DropdownMenuItem>
                <DropdownMenuItem onSelect={() => setTab("templates")}><Icon name="sparkle" size={14} />从模板创建</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>

        {tab === "mine" && (tasks === undefined
          ? <div className="mt-8 space-y-3" role="status" aria-label="正在加载任务">{[0, 1, 2].map(i => <div key={i} className="h-[84px] animate-pulse rounded-2xl bg-surface" />)}</div>
          : tasks.length === 0 ? <>
            <div className="flex flex-col items-center py-16 text-center">
              <Stopwatch />
              <p className="mt-5 text-[14px] text-muted">暂无定时任务。</p>
              <p className="mt-1 max-w-sm text-[13px] text-faint">按时间、网页变化或外部事件自动运行 Orka，结果和文件都保存在任务自己的对话里。</p>
              <Button className="mt-5" onClick={() => setEditing(blankDraft())}><Icon name="plus" size={14} />新建任务</Button>
            </div>
            <Divider />
            <TemplateGrid onPick={t => setEditing(draftFromTemplate(t))} />
          </> : visible.length === 0
            ? <p className="py-16 text-center text-[14px] text-muted">没有符合条件的任务。<button className="text-accent hover:underline" onClick={() => { setQuery(""); setKind("all"); setStatus("all"); }}>清除筛选</button></p>
            : <ul className="mt-6 flex flex-col gap-3">{visible.map(t => <TaskRow key={t.task_id} task={t} pending={!!pending[t.task_id]}
              onToggle={v => void toggle(t, v)} onEdit={() => setEditing(draftFromTask(t))} onRun={() => void runNow(t)}
              onOpen={() => onOpenConversation(t.conversation_id)} onDelete={() => void remove(t)} />)}</ul>)}
        {tab === "templates" && <TemplateGrid onPick={t => setEditing(draftFromTemplate(t))} />}
      </div>
    </div>
    {editing && <TaskEditor draft={editing} onClose={() => setEditing(null)} onSaved={() => { refreshResource("tasks"); setTab("mine"); }} />}
  </div>;
}

function statusLine(t: TaskMeta): { text: string; tone: "muted" | "warn" | "ok" } {
  if (t.disabled_reason) return { text: "已自动停用：" + t.disabled_reason, tone: "warn" };
  if (t.run_status === "running") return { text: "运行中", tone: "ok" };
  if (t.cron_status !== "on") return { text: "已停用", tone: "muted" };
  if (kindOf(t) === "event") return { text: "等待事件", tone: "muted" };
  const next = t.next_run_at ? "下次 " + formatDateTime(t.next_run_at) : "";
  if (t.last_status === "failed") return { text: ["上次失败", next].filter(Boolean).join(" · "), tone: "warn" };
  if (t.last_status === "watching" && t.last_finished_at) return { text: `已检查 ${formatDateTime(t.last_finished_at)}，尚未触发 · ${next}`, tone: "muted" };
  return { text: next, tone: "muted" };
}

function TaskRow({ task, pending, onToggle, onEdit, onRun, onOpen, onDelete }: {
  task: TaskMeta; pending: boolean; onToggle: (v: boolean) => void; onEdit: () => void; onRun: () => void; onOpen: () => void; onDelete: () => void;
}) {
  const on = task.cron_status === "on";
  const status = statusLine(task);
  return <li className={"group flex items-start gap-4 rounded-2xl border border-border bg-surface px-4 py-3.5 transition hover:border-accent/30 " + (on ? "" : "opacity-75")}>
    <span className="mt-0.5 grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-surface2 text-muted"><Icon name={KIND_ICON[kindOf(task)]} size={18} /></span>
    <div className="min-w-0 flex-1">
    <button onClick={onEdit} className="w-full min-w-0 text-left" aria-label={`编辑 ${titleOf(task)}`}>
      <h3 className="truncate text-[15px] font-medium text-ink">{titleOf(task)}</h3>
      <p className="mt-0.5 line-clamp-1 text-[13px] text-muted">{promptOf(task)}</p>
      <p className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-faint">
        <span className="inline-flex items-start gap-1"><Icon name="clock" size={12} className="mt-[3px] shrink-0" />{describeTrigger(task.trigger, task.interval_sec)}</span>
        {status.text && <span className={status.tone === "warn" ? "text-accent" : status.tone === "ok" ? "text-ok" : ""}>{status.text}</span>}
      </p>
    </button>
    <TaskRuns task={task} />
    </div>
    <div className="flex shrink-0 items-center gap-1 pt-1">
      <Switch checked={on} disabled={pending} onChange={onToggle} label={`${on ? "停用" : "启用"} ${titleOf(task)}`} />
      <DropdownMenu>
        <DropdownMenuTrigger asChild><Button variant="ghost" size="icon" aria-label={`${titleOf(task)} 的更多操作`}><span aria-hidden className="text-[18px] leading-none">⋯</span></Button></DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onSelect={onRun}><Icon name="play" size={14} />立即运行</DropdownMenuItem>
          <DropdownMenuItem onSelect={onEdit}><Icon name="rename" size={14} />编辑</DropdownMenuItem>
          <DropdownMenuItem onSelect={onOpen}><Icon name="share" size={14} />查看对话</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={onDelete} className="text-accent"><Icon name="trash" size={14} />删除</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  </li>;
}

const RECENT_RUNS = 5;
const RUN_LABEL: Record<string, string> = { done: "已完成", partial: "部分完成", failed: "失败", running: "运行中", paused: "等待确认", interrupted: "已中断" };
const RUN_DOT: Record<string, string> = {
  done: "bg-ok", partial: "border-2 border-ok", failed: "bg-accent", running: "animate-pulse bg-ok/50",
};

// TaskRuns shows a task's last few runs, oldest to newest, so whether a
// triggered task has been working is visible without opening its conversation.
function TaskRuns({ task }: { task: TaskMeta }) {
  const runs = useResource<RunRecord[]>(`runs:task:${task.task_id}`, () => api.listRuns({ task_id: task.task_id, size: RECENT_RUNS })
    .then(r => (r.runs || []).filter(run => run.task_id === task.task_id).slice(0, RECENT_RUNS)), { interval: 30000 });
  if (!runs?.length) return null;
  const finished = runs.filter(r => r.status !== "running");
  const succeeded = finished.filter(r => r.status === "done").length;
  const last = runs[0];
  const when = (r: RunRecord) => formatDateTime(r.finished_at || r.created_at);
  return <div className="mt-2 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-[12px] text-faint">
    <ol aria-label="最近运行" className="flex items-center gap-1">
      {[...runs].reverse().map(r => <li key={r.run_id} title={`${RUN_LABEL[r.status] || r.status} · ${when(r)}`} aria-label={`${RUN_LABEL[r.status] || r.status} · ${when(r)}`}
        className={"h-2.5 w-2.5 rounded-full " + (RUN_DOT[r.status] || "bg-border")} />)}
    </ol>
    <span>上次{RUN_LABEL[last.status] || "已结束"} · {when(last)}</span>
    {finished.length > 1 && <span className={succeeded === finished.length ? "text-ok" : succeeded === 0 ? "text-accent" : ""}>近 {finished.length} 次成功 {succeeded} 次</span>}
  </div>;
}

function TemplateGrid({ onPick }: { onPick: (t: TaskTemplate) => void }) {
  return <ul className="mt-6 grid gap-x-8 gap-y-2 sm:grid-cols-2">{TASK_TEMPLATES.map(t =>
    <li key={t.id}><button onClick={() => onPick(t)} aria-label={`使用模板：${t.title}`} className="flex w-full items-start gap-4 rounded-2xl px-3 py-3.5 text-left transition hover:bg-surface">
      <span className="grid h-11 w-11 shrink-0 place-items-center rounded-xl bg-surface2 text-muted"><Icon name={t.icon} size={18} /></span>
      <span className="min-w-0">
        <span className="block text-[15px] font-medium text-ink">{t.title}</span>
        <span className="mt-0.5 block text-[13px] text-muted">{t.description}</span>
        <span className="mt-1.5 inline-flex items-start gap-1 text-[12px] text-faint"><Icon name={KIND_ICON[t.trigger.kind]} size={12} className="mt-[3px] shrink-0" />{describeTrigger(t.trigger).replace(/^ /, "")}</span>
      </span>
    </button></li>)}</ul>;
}

function Divider() {
  return <svg aria-hidden className="h-3 w-full text-border" preserveAspectRatio="none" viewBox="0 0 400 12">
    <path d="M0 6 Q 5 1 10 6 T 20 6 T 30 6 T 40 6 T 50 6 T 60 6 T 70 6 T 80 6 T 90 6 T 100 6 T 110 6 T 120 6 T 130 6 T 140 6 T 150 6 T 160 6 T 170 6 T 180 6 T 190 6 T 200 6 T 210 6 T 220 6 T 230 6 T 240 6 T 250 6 T 260 6 T 270 6 T 280 6 T 290 6 T 300 6 T 310 6 T 320 6 T 330 6 T 340 6 T 350 6 T 360 6 T 370 6 T 380 6 T 390 6 T 400 6" fill="none" stroke="currentColor" strokeWidth="1" vectorEffect="non-scaling-stroke" />
  </svg>;
}

function Stopwatch() {
  return <svg aria-hidden width="88" height="100" viewBox="0 0 88 100" fill="none" className="text-ink">
    <rect x="34" y="4" width="20" height="9" rx="3" stroke="currentColor" strokeWidth="2.5" />
    <path d="M44 13v6" stroke="currentColor" strokeWidth="2.5" />
    <circle cx="44" cy="58" r="37" className="fill-surface2" stroke="currentColor" strokeWidth="2.5" />
    <path d="M44 34v24l16 9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" />
  </svg>;
}
