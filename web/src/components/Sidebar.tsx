import type { Conversation } from "../types";

export function Sidebar({
  open,
  conversations,
  activeID,
  onSelect,
  onNew,
  email,
  setEmail,
}: {
  open: boolean;
  conversations: Conversation[];
  activeID: string;
  onSelect: (id: string) => void;
  onNew: () => void;
  email: string;
  setEmail: (e: string) => void;
}) {
  return (
    <aside
      className={
        "shrink-0 overflow-hidden border-r border-border bg-surface2/60 flex flex-col transition-all duration-300 " +
        (open ? "w-[264px]" : "w-0")
      }
    >
      <div className="w-[264px] flex flex-col h-full">
        <div className="flex items-center gap-2 px-4 h-14">
          <span className="grid h-7 w-7 place-items-center rounded-lg bg-accent text-white font-serif text-[15px] leading-none">
            C
          </span>
          <span className="font-serif text-[18px] text-ink">Cavis</span>
        </div>

        <div className="px-3">
          <button
            onClick={onNew}
            className="w-full flex items-center gap-2 rounded-xl border border-border bg-surface px-3 py-2.5 text-[14px] text-ink hover:bg-surface transition hover:border-accent/40"
          >
            <span className="text-accent text-[16px] leading-none">+</span> New chat
          </button>
        </div>

        <div className="mt-4 px-3 text-[11px] uppercase tracking-wider text-faint">Recent</div>
        <div className="mt-1 flex-1 overflow-y-auto px-2 pb-2">
          {conversations.length === 0 && (
            <div className="px-2 py-2 text-[13px] text-faint">No conversations yet</div>
          )}
          {conversations.map((c) => {
            const active = c.conversation_id === activeID;
            return (
              <button
                key={c.conversation_id}
                onClick={() => onSelect(c.conversation_id)}
                className={
                  "w-full truncate rounded-lg px-3 py-2 text-left text-[14px] transition " +
                  (active ? "bg-accentsoft text-ink" : "text-muted hover:bg-surface")
                }
              >
                {c.title}
              </button>
            );
          })}
        </div>

        <div className="border-t border-border px-3 py-3">
          <label className="block text-[11px] uppercase tracking-wider text-faint mb-1">
            Signed in as
          </label>
          <input
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="w-full rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] text-ink outline-none focus:border-accent/50"
          />
        </div>
      </div>
    </aside>
  );
}
