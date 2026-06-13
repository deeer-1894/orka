import { useState } from "react";
import type { Conversation } from "../types";

export function Sidebar({
  open,
  conversations,
  activeID,
  runningIds,
  scheduledIds,
  onSelect,
  onSelectClose,
  onNew,
  onRename,
  onDelete,
  onPrune,
  name,
  email,
  onSignOut,
}: {
  open: boolean;
  conversations: Conversation[];
  activeID: string;
  runningIds: string[];
  scheduledIds: Set<string>;
  onSelect: (id: string) => void;
  onSelectClose?: () => void;
  onNew: () => void;
  onRename: (id: string, title: string) => void;
  onDelete: (id: string) => void;
  onPrune: () => void;
  name: string;
  email: string;
  onSignOut: () => void;
}) {
  const [editing, setEditing] = useState("");
  const [draft, setDraft] = useState("");
  return (
    <aside
      className={
        "fixed inset-y-0 left-0 z-40 shrink-0 overflow-hidden border-r border-border bg-surface2 md:static md:z-auto md:bg-surface2/60 flex flex-col transition-all duration-300 " +
        (open ? "w-[264px]" : "w-0")
      }
    >
      <div className="w-[264px] flex flex-col h-full">
        <div className="flex items-center gap-2 px-4 h-14">
          <span className="grid h-7 w-7 place-items-center rounded-lg bg-accent text-white font-serif text-[15px] leading-none">
            O
          </span>
          <span className="font-serif text-[18px] text-ink">Orka</span>
        </div>

        <div className="px-3">
          <button
            onClick={onNew}
            className="w-full flex items-center gap-2 rounded-xl border border-border bg-surface px-3 py-2.5 text-[14px] text-ink hover:bg-surface transition hover:border-accent/40"
          >
            <span className="text-accent text-[16px] leading-none">+</span> New chat
          </button>
        </div>

        <div className="mt-4 flex items-center justify-between px-3">
          <span className="text-[11px] uppercase tracking-wider text-faint">Recent</span>
          {conversations.length > 1 && (
            <button
              onClick={onPrune}
              className="text-[11px] text-faint hover:text-accent"
              title="删除所有没有消息的空会话"
            >
              清理空会话
            </button>
          )}
        </div>
        <div className="mt-1 flex-1 overflow-y-auto px-2 pb-2">
          {conversations.length === 0 && (
            <div className="px-2 py-2 text-[13px] text-faint">No conversations yet</div>
          )}
          {conversations.map((c) => {
            const active = c.conversation_id === activeID;
            const running = runningIds.includes(c.conversation_id);
            const scheduled = scheduledIds.has(c.conversation_id);
            if (editing === c.conversation_id) {
              return (
                <input
                  key={c.conversation_id}
                  autoFocus
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  onBlur={() => {
                    if (draft.trim()) onRename(c.conversation_id, draft.trim());
                    setEditing("");
                  }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") (e.target as HTMLInputElement).blur();
                    if (e.key === "Escape") setEditing("");
                  }}
                  className="w-full rounded-lg border border-accent/50 bg-surface px-3 py-2 text-[14px] outline-none"
                />
              );
            }
            return (
              <div
                key={c.conversation_id}
                onClick={() => {
                  onSelect(c.conversation_id);
                  onSelectClose?.();
                }}
                className={
                  "group flex items-center gap-1 rounded-lg px-3 py-2 cursor-pointer transition " +
                  (active ? "bg-accentsoft text-ink" : "text-muted hover:bg-surface")
                }
              >
                {running && (
                  <span
                    className="h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-ok"
                    title="运行中"
                  />
                )}
                {scheduled && (
                  <span className="shrink-0 text-[12px]" title="由定时任务驱动">🔁</span>
                )}
                <span className="flex-1 truncate text-[14px]" title={c.title}>{c.title}</span>
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    setDraft(c.title);
                    setEditing(c.conversation_id);
                  }}
                  className="opacity-0 group-hover:opacity-100 px-1 text-faint hover:text-ink"
                  title="重命名"
                  aria-label="重命名会话"
                >
                  ✎
                </button>
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    if (confirm("删除这个会话?")) onDelete(c.conversation_id);
                  }}
                  className="opacity-0 group-hover:opacity-100 px-1 text-faint hover:text-accent"
                  title="删除"
                  aria-label="删除会话"
                >
                  ✕
                </button>
              </div>
            );
          })}
        </div>

        <div className="flex items-center gap-2.5 border-t border-border px-3 py-3">
          <div className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-accent/90 text-[12px] text-white">
            {(name || email || "?").slice(0, 1).toUpperCase()}
          </div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px] text-ink">{name || email}</div>
            <div className="truncate text-[11px] text-faint">{email}</div>
          </div>
          <button
            onClick={onSignOut}
            className="rounded-lg px-2 py-1 text-[12px] text-faint hover:bg-surface hover:text-accent"
            title="Sign out"
          >
            ⎋
          </button>
        </div>
      </div>
    </aside>
  );
}
