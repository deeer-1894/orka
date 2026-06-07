import type { Conversation, OwnerInfo, TaskMeta } from "../types";
import { initials } from "../lib/format";

const RUN_TONE: Record<string, string> = {
  done: "var(--color-ok)",
  running: "var(--color-live)",
  failed: "var(--color-danger)",
  paused: "var(--color-clarify)",
  start: "var(--color-muted)",
};

export function LeftRail({
  conversations,
  activeID,
  onSelect,
  onNew,
  tasks,
  owners,
}: {
  conversations: Conversation[];
  activeID: string;
  onSelect: (id: string) => void;
  onNew: () => void;
  tasks: TaskMeta[];
  owners: Record<string, OwnerInfo>;
}) {
  return (
    <aside className="w-72 shrink-0 border-r hair bg-panel/40 flex flex-col">
      <Section title="conversations" action={<button onClick={onNew} className="text-live hover:brightness-110 font-mono text-[16px] leading-none">+</button>}>
        <div className="space-y-0.5">
          {conversations.length === 0 && <Hint>no conversations yet</Hint>}
          {conversations.map((c) => {
            const active = c.conversation_id === activeID;
            return (
              <button
                key={c.conversation_id}
                onClick={() => onSelect(c.conversation_id)}
                className={
                  "w-full text-left rounded-md px-2.5 py-2 transition border " +
                  (active
                    ? "border-live/30 bg-live/[0.06]"
                    : "border-transparent hover:bg-raised/40")
                }
              >
                <div className="truncate text-[13px] text-text">{c.title}</div>
                <div className="font-mono text-[10px] text-faint">
                  {c.conversation_id.slice(0, 10)} · {c.task_ids?.length ?? 0} tasks
                </div>
              </button>
            );
          })}
        </div>
      </Section>

      <Section title="tasks" grow>
        <div className="space-y-1">
          {tasks.length === 0 && <Hint>no tasks</Hint>}
          {tasks.map((t) => {
            const owner = owners[t.owner_email];
            const tone = RUN_TONE[t.run_status] ?? "var(--color-faint)";
            return (
              <div
                key={t.task_id}
                className="flex items-center gap-2.5 rounded-md border hair bg-panel2/40 px-2.5 py-2"
              >
                <div
                  className="grid h-7 w-7 shrink-0 place-items-center rounded-full font-mono text-[10px] text-ink"
                  style={{ background: "var(--color-browser)" }}
                  title={t.owner_email}
                >
                  {initials(owner?.name || t.owner_email || "?")}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[12px] text-muted">
                    {owner?.name || t.owner_email || "—"}
                  </div>
                  <div className="font-mono text-[10px] text-faint">{t.task_id.slice(0, 10)}</div>
                </div>
                <div className="flex flex-col items-end gap-1">
                  <span className="font-mono text-[9px] uppercase" style={{ color: tone }}>
                    {t.run_status}
                  </span>
                  {t.cron_status === "on" && (
                    <span className="font-mono text-[9px] text-live">⟳ cron</span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </Section>
    </aside>
  );
}

function Section({
  title,
  action,
  children,
  grow,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
  grow?: boolean;
}) {
  return (
    <div className={"flex flex-col min-h-0 " + (grow ? "flex-1" : "")}>
      <div className="flex items-center justify-between px-3 pt-3 pb-1.5">
        <span className="font-mono text-[10px] uppercase tracking-[0.22em] text-faint">{title}</span>
        {action}
      </div>
      <div className="overflow-y-auto px-2 pb-2">{children}</div>
    </div>
  );
}

function Hint({ children }: { children: React.ReactNode }) {
  return <div className="px-2.5 py-2 font-mono text-[11px] text-faint/60">{children}</div>;
}
