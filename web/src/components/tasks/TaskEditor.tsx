import { useState, type ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { api, webhookURL, type TaskInput } from "../../api";
import type { TaskMeta } from "../../types";
import {
  CHECK_INTERVALS, FREQUENCIES, TRIGGER_KINDS, WEEKDAYS, describeTrigger, localTimezone, scheduleForm,
  scheduleTrigger, defaultScheduleForm, type ScheduleForm, type TaskTrigger, type TriggerKind,
} from "../../lib/taskTrigger";
import { toast } from "../../lib/toast";
import { Button } from "../ui/button";
import { Icon, type IconName } from "../Icon";

export interface TaskDraft {
  task_id?: string;
  title: string;
  prompt: string;
  trigger: TaskTrigger;
  enabled: boolean;
  retry_count: number;
  webhook_token?: string;
}

export function draftFromTask(t: TaskMeta): TaskDraft {
  const vars = t.variables || {};
  return {
    task_id: t.task_id,
    title: String(vars.title || ""),
    prompt: String(vars.prompt_template || vars.prompt || ""),
    trigger: t.trigger || { kind: "schedule", cron: "0 9 * * *" },
    enabled: t.cron_status === "on",
    retry_count: t.retry_count || 0,
    webhook_token: t.webhook_token,
  };
}

export const KIND_ICON: Record<TriggerKind, IconName> = { schedule: "clock", condition: "eye", event: "plug" };

const field = "w-full rounded-lg border border-border bg-bg px-3 py-2 text-[14px] text-ink outline-none transition focus:border-accent/60";
const chip = (on: boolean) =>
  "rounded-lg border px-3 py-1.5 text-[13px] transition " + (on ? "border-accent/40 bg-accentsoft text-accent" : "border-border text-muted hover:bg-surface2");

function Field({ label, hint, htmlFor, children }: { label: string; hint?: string; htmlFor?: string; children: ReactNode }) {
  return <div className="flex flex-col gap-1.5">
    <label htmlFor={htmlFor} className="text-[13px] font-medium text-ink">{label}</label>
    {children}
    {hint && <p className="text-[12px] leading-relaxed text-faint">{hint}</p>}
  </div>;
}

export function Switch({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return <button type="button" role="switch" aria-checked={checked} aria-label={label} disabled={disabled} onClick={() => onChange(!checked)}
    className={"relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition disabled:opacity-40 " + (checked ? "bg-accent" : "bg-border")}>
    <span className={"inline-block h-4 w-4 rounded-full bg-white shadow transition " + (checked ? "translate-x-[18px]" : "translate-x-0.5")} />
  </button>;
}

// TaskEditor creates or edits a task. Event tasks show their webhook address
// right after creation, since that is the next thing the user has to do.
export function TaskEditor({ draft: initial, onClose, onSaved }: { draft: TaskDraft; onClose: () => void; onSaved: (t: TaskMeta) => void }) {
  const [draft, setDraft] = useState(initial);
  const [schedule, setSchedule] = useState<ScheduleForm>(() => initial.trigger.kind === "schedule" ? scheduleForm(initial.trigger) : defaultScheduleForm());
  const [condition, setCondition] = useState(() => ({
    watch_url: initial.trigger.watch_url || "", contains: initial.trigger.contains || "",
    mode: initial.trigger.contains ? "contains" : "change", check_every_sec: initial.trigger.check_every_sec || 3600,
  }));
  const [event, setEvent] = useState(initial.trigger.event || "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<TaskMeta | null>(null);
  const kind = draft.trigger.kind;
  const patch = (p: Partial<TaskDraft>) => { setDraft(d => ({ ...d, ...p })); setError(""); };
  const patchSchedule = (p: Partial<ScheduleForm>) => { setSchedule(s => ({ ...s, ...p })); setError(""); };

  const buildTrigger = (): TaskTrigger => {
    if (kind === "schedule") return scheduleTrigger(schedule);
    if (kind === "condition") {
      const url = condition.watch_url.trim();
      if (!/^https?:\/\/[^/\s]+\.[^\s]+/.test(url)) throw new Error("请填写要监控的完整网址，例如 https://example.com/page");
      if (condition.mode === "contains" && !condition.contains.trim()) throw new Error("请填写要等待出现的文字");
      return { kind, watch_url: url, contains: condition.mode === "contains" ? condition.contains.trim() : "", check_every_sec: condition.check_every_sec };
    }
    return { kind: "event", event: event.trim() };
  };
  let preview = "";
  try { preview = describeTrigger(buildTrigger()); } catch { preview = ""; }

  const save = async () => {
    let trigger: TaskTrigger;
    try {
      if (!draft.prompt.trim()) throw new Error("请填写任务要做什么");
      if (/【[^】]*】/.test(draft.prompt)) throw new Error("请把【】中的内容换成你的实际信息");
      trigger = buildTrigger();
    } catch (e) { setError((e as Error).message); return; }
    setBusy(true);
    try {
      const input: TaskInput = { task_id: draft.task_id, title: draft.title.trim(), prompt: draft.prompt.trim(), trigger, enabled: draft.enabled, retry_count: draft.retry_count };
      const saved = await api.saveTask(input);
      onSaved(saved);
      if (!draft.task_id && trigger.kind === "event" && saved.webhook_token) setCreated(saved);
      else { toast(draft.task_id ? "已保存" : "任务已创建", "success"); onClose(); }
    } catch (e) {
      setError((e as Error).message || "保存失败，请重试");
    } finally { setBusy(false); }
  };

  const token = created?.webhook_token || draft.webhook_token;
  return <Dialog.Root open onOpenChange={open => { if (!open && !busy) onClose(); }}>
    <Dialog.Portal>
      <Dialog.Overlay className="overlay-in fixed inset-0 z-50 bg-black/40" />
      <Dialog.Content className="pop-in fixed left-1/2 top-1/2 z-50 flex max-h-[92vh] w-[min(620px,calc(100vw-24px))] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-2xl border border-border bg-surface shadow-xl">
        <div className="flex items-center justify-between gap-4 border-b border-border px-6 py-4">
          <Dialog.Title className="font-serif text-[20px] text-ink">{created ? "任务已创建" : draft.task_id ? "编辑任务" : "新建任务"}</Dialog.Title>
          <Dialog.Close asChild><Button variant="ghost" size="icon" disabled={busy} aria-label="关闭"><Icon name="close" size={16} /></Button></Dialog.Close>
        </div>
        <Dialog.Description className="sr-only">设置任务内容与触发条件</Dialog.Description>
        {created ? <div className="flex flex-col gap-4 px-6 py-5">
          <p className="text-[14px] text-muted">把下面的地址配置到外部系统（例如 GitHub 仓库的 Settings → Webhooks，Content type 选 application/json）。收到{event.trim() ? `“${event.trim()}”` : "任意"}事件时，Orka 会运行这个任务。</p>
          <WebhookAddress token={created.webhook_token!} />
          <div className="flex justify-end"><Button onClick={() => { toast("任务已创建", "success"); onClose(); }}>完成</Button></div>
        </div> : <>
          <form id="task-editor" className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto px-6 py-5" onSubmit={e => { e.preventDefault(); void save(); }}>
            <Field label="名称" htmlFor="task-title" hint="留空时取任务内容的开头。">
              <input id="task-title" className={field} value={draft.title} maxLength={80} placeholder="例如：每日资讯简报" onChange={e => patch({ title: e.target.value })} />
            </Field>
            <Field label="要做什么" htmlFor="task-prompt" hint="像发一条消息一样描述。每次触发都在这个任务自己的对话里执行，产出的文件也保存在那里。">
              <textarea id="task-prompt" className={field + " min-h-[112px] resize-y leading-relaxed"} value={draft.prompt} maxLength={8000} placeholder="例如：搜索 AI 智能体领域过去 24 小时的重要新闻，整理成 5 条要点简报，附原文链接。" onChange={e => patch({ prompt: e.target.value })} />
            </Field>

            <div className="flex flex-col gap-2">
              <span className="text-[13px] font-medium text-ink">什么时候运行</span>
              <div role="radiogroup" aria-label="触发方式" className="grid grid-cols-3 gap-2">
                {TRIGGER_KINDS.map(k => <button key={k.id} type="button" role="radio" aria-checked={kind === k.id}
                  onClick={() => patch({ trigger: { ...draft.trigger, kind: k.id } })}
                  className={"flex items-center justify-center gap-1.5 rounded-xl border px-3 py-2.5 text-[13px] transition " + (kind === k.id ? "border-accent/50 bg-accentsoft text-accent" : "border-border text-muted hover:bg-surface2")}>
                  <Icon name={KIND_ICON[k.id]} size={14} />{k.label}
                </button>)}
              </div>
              <p className="text-[12px] text-faint">{TRIGGER_KINDS.find(k => k.id === kind)?.hint}</p>
            </div>

            {kind === "schedule" && <div className="flex flex-col gap-4 rounded-xl border border-border bg-bg/40 p-4">
              <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="频率">
                {FREQUENCIES.map(f => <button key={f.id} type="button" role="radio" aria-checked={schedule.frequency === f.id} className={chip(schedule.frequency === f.id)} onClick={() => patchSchedule({ frequency: f.id })}>{f.label}</button>)}
              </div>
              {schedule.frequency === "once" && <Field label="执行时间" htmlFor="task-once"><input id="task-once" type="datetime-local" className={field} value={schedule.onceAt} onChange={e => patchSchedule({ onceAt: e.target.value })} /></Field>}
              {schedule.frequency === "hourly" && <Field label="每小时的第几分钟" htmlFor="task-minute"><input id="task-minute" type="number" min={0} max={59} className={field + " w-28"} value={schedule.minute} onChange={e => patchSchedule({ minute: Number(e.target.value) })} /></Field>}
              {schedule.frequency === "weekly" && <div className="flex flex-col gap-1.5">
                <span className="text-[13px] font-medium text-ink">星期</span>
                <div className="flex flex-wrap gap-1.5">{WEEKDAYS.map(w => {
                  const on = schedule.weekdays.includes(w.day);
                  return <button key={w.day} type="button" aria-pressed={on} aria-label={"星期" + w.label} className={chip(on) + " w-10 px-0"}
                    onClick={() => patchSchedule({ weekdays: on ? schedule.weekdays.filter(d => d !== w.day) : [...schedule.weekdays, w.day] })}>{w.label}</button>;
                })}</div>
              </div>}
              {schedule.frequency === "monthly" && <Field label="每月几号" htmlFor="task-day" hint="1–28 号，保证每个月都会运行。"><input id="task-day" type="number" min={1} max={28} className={field + " w-28"} value={schedule.monthDay} onChange={e => patchSchedule({ monthDay: Number(e.target.value) })} /></Field>}
              {["daily", "weekdays", "weekly", "monthly"].includes(schedule.frequency) && <Field label="时间" htmlFor="task-time"><input id="task-time" type="time" className={field + " w-36"} value={schedule.time} onChange={e => patchSchedule({ time: e.target.value })} /></Field>}
              {schedule.frequency === "custom" && <Field label="Cron 表达式" htmlFor="task-cron" hint="5 段：分 时 日 月 周。例如 */30 9-18 * * 1-5 表示工作日 9–18 点每半小时。">
                <input id="task-cron" className={field + " font-mono"} value={schedule.cron} placeholder="0 9 * * 1-5" onChange={e => patchSchedule({ cron: e.target.value })} />
              </Field>}
            </div>}

            {kind === "condition" && <div className="flex flex-col gap-4 rounded-xl border border-border bg-bg/40 p-4">
              <Field label="监控的网页" htmlFor="task-url" hint="适合内容相对稳定的页面；广告、计数器很多的页面可能频繁触发。">
                <input id="task-url" className={field} inputMode="url" value={condition.watch_url} placeholder="https://example.com/page" onChange={e => { setCondition(c => ({ ...c, watch_url: e.target.value })); setError(""); }} />
              </Field>
              <div className="flex flex-col gap-1.5">
                <span className="text-[13px] font-medium text-ink">满足什么条件</span>
                <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="条件">
                  <button type="button" role="radio" aria-checked={condition.mode === "change"} className={chip(condition.mode === "change")} onClick={() => setCondition(c => ({ ...c, mode: "change" }))}>页面内容变化</button>
                  <button type="button" role="radio" aria-checked={condition.mode === "contains"} className={chip(condition.mode === "contains")} onClick={() => setCondition(c => ({ ...c, mode: "contains" }))}>出现指定文字</button>
                </div>
              </div>
              {condition.mode === "contains" && <Field label="等待出现的文字" htmlFor="task-contains" hint="文字从无到有时运行一次；之后消失再出现会再次运行。">
                <input id="task-contains" className={field} value={condition.contains} maxLength={200} placeholder="例如：有货、已发布" onChange={e => { setCondition(c => ({ ...c, contains: e.target.value })); setError(""); }} />
              </Field>}
              <Field label="检查频率" htmlFor="task-every">
                <select id="task-every" className={field + " w-44"} value={condition.check_every_sec} onChange={e => setCondition(c => ({ ...c, check_every_sec: Number(e.target.value) }))}>
                  {CHECK_INTERVALS.map(i => <option key={i.sec} value={i.sec}>{i.label}</option>)}
                </select>
              </Field>
            </div>}

            {kind === "event" && <div className="flex flex-col gap-4 rounded-xl border border-border bg-bg/40 p-4">
              <Field label="只响应这些事件（可选）" htmlFor="task-event" hint="GitHub 事件写作“事件.动作”，留空则任何调用都会运行。其他系统可在请求体里带 event 字段。">
                <input id="task-event" className={field + " font-mono"} value={event} maxLength={100} placeholder="pull_request.closed" onChange={e => { setEvent(e.target.value); setError(""); }} />
              </Field>
              <div className="flex flex-wrap gap-1.5">{["pull_request.closed", "push", "issues.opened", "release.published"].map(e =>
                <button key={e} type="button" className={chip(event === e) + " font-mono text-[12px]"} onClick={() => setEvent(e)}>{e}</button>)}</div>
              {token ? <WebhookAddress token={token} /> : <p className="text-[12px] text-faint">保存后生成专属 Webhook 地址。</p>}
            </div>}

            <Field label="失败后自动重试" htmlFor="task-retry">
              <select id="task-retry" className={field + " w-32"} value={draft.retry_count} onChange={e => patch({ retry_count: Number(e.target.value) })}>
                {[0, 1, 2, 3].map(n => <option key={n} value={n}>{n === 0 ? "不重试" : `${n} 次`}</option>)}
              </select>
            </Field>
            {error && <p role="alert" className="rounded-lg bg-accentsoft px-3 py-2 text-[13px] text-accent">{error}</p>}
          </form>
          <div className="flex flex-wrap items-center gap-3 border-t border-border px-6 py-4">
            <label className="flex items-center gap-2 text-[13px] text-muted"><Switch checked={draft.enabled} onChange={v => patch({ enabled: v })} label="启用任务" />启用</label>
            {preview && <span className="min-w-0 flex-1 truncate text-[12px] text-faint" title={preview}>{preview}{kind === "schedule" && localTimezone() ? ` · ${localTimezone()}` : ""}</span>}
            <div className="ml-auto flex gap-2">
              <Button type="button" variant="ghost" disabled={busy} onClick={onClose}>取消</Button>
              <Button type="submit" form="task-editor" disabled={busy}>{busy ? "保存中…" : "保存"}</Button>
            </div>
          </div>
        </>}
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}

export function WebhookAddress({ token }: { token: string }) {
  const url = webhookURL(token);
  const copy = () => navigator.clipboard?.writeText(url).then(() => toast("已复制 Webhook 地址", "success"), () => toast("复制失败，请手动选择", "error"));
  return <div className="flex flex-col gap-1.5">
    <span className="text-[13px] font-medium text-ink">Webhook 地址</span>
    <div className="flex items-center gap-2">
      <code className="min-w-0 flex-1 truncate rounded-lg border border-border bg-bg px-3 py-2 text-[12px] text-muted" title={url}>{url}</code>
      <Button type="button" variant="outline" size="sm" onClick={copy}><Icon name="copy" size={14} />复制</Button>
    </div>
    <p className="text-[12px] text-faint">地址本身即凭证，请勿公开。用 POST 调用即可触发。</p>
  </div>;
}
