import { useState } from "react";
import type { RunStatus } from "../hooks/useChatStream";

const TOOLS = [
  { id: "search", label: "search" },
  { id: "file", label: "file" },
  { id: "gui_agent", label: "gui" },
];

export function Composer({
  status,
  enabled,
  setEnabled,
  onSend,
  onKill,
}: {
  status: RunStatus;
  enabled: string[];
  setEnabled: (t: string[]) => void;
  onSend: (msg: string) => void;
  onKill: () => void;
}) {
  const [text, setText] = useState("");
  const busy = status === "streaming";

  const send = () => {
    if (!text.trim() || busy) return;
    onSend(text.trim());
    setText("");
  };
  const toggle = (id: string) =>
    setEnabled(enabled.includes(id) ? enabled.filter((x) => x !== id) : [...enabled, id]);

  return (
    <div className="px-5 pb-5">
      <div className="mx-auto max-w-3xl">
        <div className="rounded-[26px] border border-border bg-surface shadow-[0_2px_18px_rgba(40,38,32,0.06)] focus-within:border-accent/40 transition">
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                send();
              }
            }}
            rows={1}
            placeholder="Message Cavis…"
            className="block w-full resize-none bg-transparent px-5 pt-4 pb-1 text-[15px] outline-none placeholder:text-faint max-h-48"
          />
          <div className="flex items-center gap-2 px-3 pb-3 pt-1">
            {TOOLS.map((t) => {
              const on = enabled.includes(t.id);
              return (
                <button
                  key={t.id}
                  onClick={() => toggle(t.id)}
                  className={
                    "rounded-full border px-3 py-1 text-[13px] transition " +
                    (on
                      ? "border-accent/40 bg-accentsoft text-accent"
                      : "border-border text-muted hover:bg-surface2")
                  }
                >
                  {t.label}
                </button>
              );
            })}
            <div className="ml-auto">
              {busy ? (
                <button
                  onClick={onKill}
                  className="grid h-9 w-9 place-items-center rounded-full bg-ink text-bg hover:opacity-80 transition"
                  title="Stop"
                >
                  <span className="h-3 w-3 rounded-[3px] bg-bg" />
                </button>
              ) : (
                <button
                  onClick={send}
                  disabled={!text.trim()}
                  className="grid h-9 w-9 place-items-center rounded-full bg-accent text-white hover:brightness-105 disabled:opacity-30 transition"
                  title="Send"
                >
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
                    <path d="M12 19V5M12 5l-6 6M12 5l6 6" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
                  </svg>
                </button>
              )}
            </div>
          </div>
        </div>
        <p className="mt-2 text-center text-[11px] text-faint">
          Cavis can make mistakes. Tool actions run against your workspace.
        </p>
      </div>
    </div>
  );
}
