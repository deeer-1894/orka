// Task triggers: the editor's schedule form, its cron/one-off encoding, and
// the one-line summaries shown on task cards. Pure functions, unit-tested.

export type TriggerKind = "schedule" | "condition" | "event";

export interface TaskTrigger {
  kind: TriggerKind;
  cron?: string;
  at?: number;
  timezone?: string;
  watch_url?: string;
  contains?: string;
  check_every_sec?: number;
  event?: string;
}

export type Frequency = "once" | "hourly" | "daily" | "weekdays" | "weekly" | "monthly" | "custom";

export interface ScheduleForm {
  frequency: Frequency;
  time: string; // HH:MM
  minute: number; // hourly
  weekdays: number[]; // cron numbering, 0 = Sunday
  monthDay: number;
  onceAt: string; // datetime-local value
  cron: string;
}

export const FREQUENCIES: { id: Frequency; label: string }[] = [
  { id: "once", label: "仅一次" },
  { id: "hourly", label: "每小时" },
  { id: "daily", label: "每天" },
  { id: "weekdays", label: "工作日" },
  { id: "weekly", label: "每周" },
  { id: "monthly", label: "每月" },
  { id: "custom", label: "自定义 Cron" },
];

export const CHECK_INTERVALS: { sec: number; label: string }[] = [
  { sec: 300, label: "每 5 分钟" },
  { sec: 900, label: "每 15 分钟" },
  { sec: 1800, label: "每 30 分钟" },
  { sec: 3600, label: "每小时" },
  { sec: 21600, label: "每 6 小时" },
  { sec: 86400, label: "每天" },
];

// Monday-first display order, in cron numbering.
export const WEEKDAYS: { day: number; label: string }[] = [
  { day: 1, label: "一" }, { day: 2, label: "二" }, { day: 3, label: "三" }, { day: 4, label: "四" },
  { day: 5, label: "五" }, { day: 6, label: "六" }, { day: 0, label: "日" },
];

const pad = (n: number) => String(n).padStart(2, "0");
const dayName = (d: number) => "星期" + (WEEKDAYS.find(w => w.day === d)?.label ?? "?");

export function localTimezone(): string {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || ""; } catch { return ""; }
}

export function defaultScheduleForm(now = new Date()): ScheduleForm {
  const next = new Date(now.getTime() + 3600_000);
  const local = `${next.getFullYear()}-${pad(next.getMonth() + 1)}-${pad(next.getDate())}T${pad(next.getHours())}:00`;
  return { frequency: "weekdays", time: "09:00", minute: 0, weekdays: [1], monthDay: 1, onceAt: local, cron: "0 9 * * 1-5" };
}

function parseTime(time: string): [number, number] {
  const [h, m] = time.split(":").map(Number);
  return [Number.isInteger(h) && h >= 0 && h < 24 ? h : 9, Number.isInteger(m) && m >= 0 && m < 60 ? m : 0];
}

// scheduleTrigger compiles the form; it throws a user-facing message when invalid.
export function scheduleTrigger(f: ScheduleForm, timezone = localTimezone()): TaskTrigger {
  const [h, m] = parseTime(f.time);
  const base = { kind: "schedule" as const, timezone };
  switch (f.frequency) {
    case "once": {
      const at = new Date(f.onceAt).getTime();
      if (!Number.isFinite(at)) throw new Error("请选择执行时间");
      return { kind: "schedule", at };
    }
    case "hourly": return { ...base, cron: `${Math.min(59, Math.max(0, Math.round(f.minute)))} * * * *` };
    case "daily": return { ...base, cron: `${m} ${h} * * *` };
    case "weekdays": return { ...base, cron: `${m} ${h} * * 1-5` };
    case "weekly": {
      if (!f.weekdays.length) throw new Error("请至少选择一天");
      return { ...base, cron: `${m} ${h} * * ${[...new Set(f.weekdays)].sort((a, b) => a - b).join(",")}` };
    }
    case "monthly": return { ...base, cron: `${m} ${h} ${Math.min(28, Math.max(1, Math.round(f.monthDay)))} * *` };
    case "custom": {
      if (f.cron.trim().split(/\s+/).length !== 5) throw new Error("Cron 需要 5 段：分 时 日 月 周");
      return { ...base, cron: f.cron.trim() };
    }
  }
}

// scheduleForm reverses scheduleTrigger for editing; unknown crons stay custom.
export function scheduleForm(t: TaskTrigger, now = new Date()): ScheduleForm {
  const f = defaultScheduleForm(now);
  if (t.at) {
    const d = new Date(t.at);
    return { ...f, frequency: "once", onceAt: `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}` };
  }
  const cron = (t.cron || "").trim();
  f.cron = cron;
  const hourly = cron.match(/^(\d{1,2}) \* \* \* \*$/);
  if (hourly) return { ...f, frequency: "hourly", minute: Number(hourly[1]) };
  const fixed = cron.match(/^(\d{1,2}) (\d{1,2}) (\*|\d{1,2}) \* (\*|1-5|[0-6](?:,[0-6])*)$/);
  if (!fixed) return { ...f, frequency: "custom" };
  const [, m, h, dom, dow] = fixed;
  const time = `${pad(Number(h))}:${pad(Number(m))}`;
  if (dom !== "*") return dow === "*" ? { ...f, frequency: "monthly", time, monthDay: Number(dom) } : { ...f, frequency: "custom" };
  if (dow === "*") return { ...f, frequency: "daily", time };
  if (dow === "1-5") return { ...f, frequency: "weekdays", time };
  return { ...f, frequency: "weekly", time, weekdays: dow.split(",").map(Number) };
}

function intervalLabel(sec: number): string {
  const known = CHECK_INTERVALS.find(i => i.sec === sec);
  if (known) return known.label;
  if (sec % 86400 === 0) return `每 ${sec / 86400} 天`;
  if (sec % 3600 === 0) return `每 ${sec / 3600} 小时`;
  return `每 ${Math.round(sec / 60)} 分钟`;
}

function hostOf(url = ""): string {
  try { return new URL(url).host || "网页"; } catch { return "网页"; }
}

export function formatDateTime(ms: number, now = new Date()): string {
  const d = new Date(ms);
  const time = `${d.getHours()}:${pad(d.getMinutes())}`;
  const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day(d) - day(now)) / 86400_000);
  if (diff === 0) return `今天 ${time}`;
  if (diff === 1) return `明天 ${time}`;
  if (diff === -1) return `昨天 ${time}`;
  return `${d.getMonth() + 1}月${d.getDate()}日 ${time}`;
}

// describeTrigger is the card's one-line "when"; legacy tasks show their interval.
export function describeTrigger(t: TaskTrigger | undefined | null, intervalSec = 0, now = new Date()): string {
  if (!t) return intervalSec > 0 ? intervalLabel(intervalSec) : "未设置触发条件";
  if (t.kind === "condition") {
    const what = t.contains ? `出现“${t.contains}”时` : "内容变化时";
    return `${hostOf(t.watch_url)} ${what} · ${intervalLabel(t.check_every_sec || 3600)}检查`;
  }
  if (t.kind === "event") return t.event ? `由 ${t.event} 触发` : "由 Webhook 调用触发";
  if (t.at) return `仅一次 · ${formatDateTime(t.at, now)}`;
  const f = scheduleForm(t, now);
  const [h, m] = parseTime(f.time);
  const time = `${h}:${pad(m)}`;
  switch (f.frequency) {
    case "hourly": return `每小时第 ${f.minute} 分`;
    case "daily": return `每天 ${time}`;
    case "weekdays": return `工作日 ${time}`;
    case "weekly": return `每${f.weekdays.length === 7 ? "天" : WEEKDAYS.filter(w => f.weekdays.includes(w.day)).map(w => dayName(w.day)).join("、")} ${time}`;
    case "monthly": return `每月 ${f.monthDay} 日 ${time}`;
    default: return `Cron ${t.cron}`;
  }
}

export const TRIGGER_KINDS: { id: TriggerKind; label: string; hint: string }[] = [
  { id: "schedule", label: "时间", hint: "按日程运行：每天、工作日、每周，或只运行一次" },
  { id: "condition", label: "条件", hint: "定期检查网页，内容变化或出现指定文字时运行" },
  { id: "event", label: "事件", hint: "外部系统调用 Webhook 时运行，例如 GitHub 的 PR 关闭" },
];
