import { useState } from "react";
import type { RunStatus } from "../hooks/useChatStream";

const TOOLS = [
  { id: "file", label: "file", color: "var(--color-ok)" },
  { id: "gui_agent", label: "gui", color: "var(--color-browser)" },
];

export function Composer({
  status,
  enabled,
  setEnabled,
  email,
  setEmail,
  onSend,
  onKill,
}: {
  status: RunStatus;
  enabled: string[];
  setEnabled: (t: string[]) => void;
  email: string;
  setEmail: (e: string) => void;
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
    <div className="border-t hair bg-panel/70 backdrop-blur-sm px-6 py-3">
      <div className="mx-auto max-w-3xl">
        <div className="mb-2 flex items-center gap-3">
          {TOOLS.map((t) => {
            const on = enabled.includes(t.id);
            return (
              <button
                key={t.id}
                onClick={() => toggle(t.id)}
                className="font-mono text-[10px] uppercase tracking-[0.16em] px-2 py-1 rounded border transition"
                style={{
                  color: on ? t.color : "var(--color-faint)",
                  borderColor: on ? t.color : "var(--color-line)",
                  background: on ? "rgba(255,255,255,0.04)" : "transparent",
                }}
              >
                {on ? "▣" : "▢"} {t.label}
              </button>
            );
          })}
          <span className="ml-auto flex items-center gap-1.5">
            <span className="font-mono text-[10px] text-faint">as</span>
            <input
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="w-40 bg-transparent font-mono text-[11px] text-muted outline-none border-b hair focus:border-live/40"
            />
          </span>
        </div>

        <div className="flex items-end gap-2">
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
            placeholder="message the agent…  (⏎ send · ⇧⏎ newline)"
            className="flex-1 resize-none rounded-lg border hair bg-panel px-4 py-3 text-[14px] outline-none focus:border-live/40 placeholder:text-faint/60"
          />
          {busy ? (
            <button
              onClick={onKill}
              className="h-11 rounded-lg border border-danger/50 bg-danger/10 px-4 font-mono text-[12px] text-danger hover:bg-danger/20 transition"
            >
              kill
            </button>
          ) : (
            <button
              onClick={send}
              className="h-11 rounded-lg bg-live px-5 font-display font-bold text-[13px] text-ink hover:brightness-110 transition"
            >
              run →
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
