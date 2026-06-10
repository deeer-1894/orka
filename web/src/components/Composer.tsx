import { useState } from "react";
import type { RunStatus } from "../hooks/useChatStream";

export function Composer({
  status,
  onSend,
  onKill,
}: {
  status: RunStatus;
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

  return (
    <div className="px-5 pb-5">
      <div className="mx-auto max-w-3xl">
        <div className="flex items-end gap-2 rounded-[26px] border border-border bg-surface px-2 py-2 shadow-[0_2px_18px_rgba(40,38,32,0.06)] focus-within:border-accent/40 transition">
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
            placeholder="Message Orka…"
            className="block max-h-48 flex-1 resize-none bg-transparent px-3 py-2 text-[15px] outline-none placeholder:text-faint"
          />
          {busy ? (
            <button
              onClick={onKill}
              className="grid h-10 w-10 shrink-0 place-items-center rounded-full bg-ink text-bg hover:opacity-80 transition"
              title="Stop"
            >
              <span className="h-3 w-3 rounded-[3px] bg-bg" />
            </button>
          ) : (
            <button
              onClick={send}
              disabled={!text.trim()}
              className="grid h-10 w-10 shrink-0 place-items-center rounded-full bg-accent text-white hover:brightness-105 disabled:opacity-30 transition"
              title="Send"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
                <path d="M12 19V5M12 5l-6 6M12 5l6 6" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
          )}
        </div>
        <p className="mt-2 text-center text-[11px] text-faint">
          Orka 会自动选择工具(搜索 · 网页 · 天气 · 文件 · 浏览器)。可能出错,工具操作会作用于你的工作区。
        </p>
      </div>
    </div>
  );
}
