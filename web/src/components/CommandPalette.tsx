import { useMemo, useRef, useState } from "react";
import { useOverlay } from "../lib/useOverlay";

export type Command = {
  id: string;
  label: string;
  hint?: string;
  icon?: string;
  group: string;
  keywords?: string;
  run: () => void;
};

// CommandPalette is the single entry point for the app's scattered actions
// (jump conversation, switch model, open a panel, run/share, settings). ⌘K.
export function CommandPalette({ commands, onClose }: { commands: Command[]; onClose: () => void }) {
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  useOverlay(onClose);

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase();
    if (!s) return commands;
    return commands.filter((c) => (c.label + " " + (c.hint || "") + " " + (c.keywords || "") + " " + c.group).toLowerCase().includes(s));
  }, [q, commands]);

  // Stable group order as first-seen in the (filtered) list.
  const groups: { name: string; items: Command[] }[] = [];
  for (const c of filtered) {
    let g = groups.find((x) => x.name === c.group);
    if (!g) { g = { name: c.group, items: [] }; groups.push(g); }
    g.items.push(c);
  }
  const flat = groups.flatMap((g) => g.items); // selection index space

  const choose = (c: Command | undefined) => { if (c) { onClose(); c.run(); } };

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setSel((i) => Math.min(i + 1, flat.length - 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setSel((i) => Math.max(i - 1, 0)); }
    else if (e.key === "Enter") { e.preventDefault(); choose(flat[sel]); }
  };

  let idx = -1;
  return (
    <div className="overlay-in fixed inset-0 z-[60] flex items-start justify-center bg-black/30 p-6 pt-[12vh]" onClick={onClose}>
      <div className="pop-in flex max-h-[70vh] w-full max-w-xl flex-col overflow-hidden rounded-2xl border border-border bg-surface shadow-xl" onClick={(e) => e.stopPropagation()}>
        <input
          autoFocus
          value={q}
          onChange={(e) => { setQ(e.target.value); setSel(0); }}
          onKeyDown={onKey}
          placeholder="搜索命令、会话、面板…"
          className="border-b border-border bg-transparent px-4 py-3 text-[15px] outline-none placeholder:text-faint"
        />
        <div ref={listRef} className="flex-1 overflow-y-auto py-1.5">
          {flat.length === 0 && <div className="px-4 py-6 text-center text-[13px] text-faint">没有匹配的命令</div>}
          {groups.map((g) => (
            <div key={g.name} className="mb-1">
              <div className="px-4 pb-0.5 pt-1.5 text-[10.5px] uppercase tracking-wider text-faint/80">{g.name}</div>
              {g.items.map((c) => {
                idx++;
                const active = idx === sel;
                const myIdx = idx;
                return (
                  <button
                    key={c.id}
                    onMouseEnter={() => setSel(myIdx)}
                    onClick={() => choose(c)}
                    className={"flex w-full items-center gap-2.5 px-4 py-2 text-left " + (active ? "bg-accentsoft" : "hover:bg-surface2")}
                  >
                    <span className="w-5 shrink-0 text-center text-[14px]">{c.icon || "·"}</span>
                    <span className="min-w-0 flex-1 truncate text-[13.5px] text-ink">{c.label}</span>
                    {c.hint && <span className="shrink-0 truncate text-[11.5px] text-faint">{c.hint}</span>}
                  </button>
                );
              })}
            </div>
          ))}
        </div>
        <div className="flex items-center gap-3 border-t border-border px-4 py-1.5 text-[11px] text-faint">
          <span>↑↓ 选择</span><span>↵ 执行</span><span>esc 关闭</span>
        </div>
      </div>
    </div>
  );
}
