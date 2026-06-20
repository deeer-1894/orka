import { useState } from "react";
import { api } from "../api";
import type { Conversation, ConversationShare } from "../types";
import { toast, toastError } from "../lib/toast";

// ShareDialog lets a conversation owner grant/revoke access by email. viewer =
// read-only; editor = may also send (running in the owner's workspace).
export function ShareDialog({ conv, onClose, onChanged }: { conv: Conversation; onClose: () => void; onChanged: (shares: ConversationShare[]) => void }) {
  const [shares, setShares] = useState<ConversationShare[]>(conv.shares ?? []);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<"viewer" | "editor">("viewer");
  const [busy, setBusy] = useState(false);

  const apply = async (e: string, r: "viewer" | "editor" | "none") => {
    setBusy(true);
    try {
      const res = await api.shareConversation(conv.conversation_id, e, r);
      const next = (res.shares ?? []) as ConversationShare[];
      setShares(next);
      onChanged(next);
    } catch {
      toastError("操作失败");
    } finally {
      setBusy(false);
    }
  };

  const add = async () => {
    const e = email.trim().toLowerCase();
    if (!e || !e.includes("@")) return toastError("请输入有效邮箱");
    await apply(e, role);
    setEmail("");
    toast("已分享给 " + e);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
      <div className="w-full max-w-md rounded-2xl border border-border bg-surface p-4 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="mb-1 flex items-center gap-2">
          <span className="text-[15px]">🔗</span>
          <span className="flex-1 truncate text-[15px] font-medium text-ink">分享会话</span>
          <button onClick={onClose} className="text-faint hover:text-ink">✕</button>
        </div>
        <p className="mb-3 truncate text-[12px] text-faint">{conv.title}</p>

        <div className="mb-3 flex gap-1.5">
          <input
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && add()}
            placeholder="对方邮箱…"
            className="min-w-0 flex-1 rounded-lg border border-border bg-surface2 px-2.5 py-1.5 text-[13px] outline-none focus:border-accent/50"
          />
          <select
            value={role}
            onChange={(e) => setRole(e.target.value as "viewer" | "editor")}
            className="rounded-lg border border-border bg-surface2 px-2 py-1.5 text-[13px] text-ink outline-none"
          >
            <option value="viewer">只读</option>
            <option value="editor">可编辑</option>
          </select>
          <button onClick={add} disabled={busy} className="rounded-lg bg-accent px-3 py-1.5 text-[13px] text-white hover:opacity-90 disabled:opacity-50">
            添加
          </button>
        </div>

        {shares.length === 0 ? (
          <div className="rounded-lg bg-surface2/50 px-3 py-4 text-center text-[12.5px] text-faint">尚未分享给任何人</div>
        ) : (
          <div className="flex flex-col gap-1">
            {shares.map((s) => (
              <div key={s.email} className="flex items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface2">
                <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{s.email}</span>
                <select
                  value={s.role}
                  onChange={(e) => apply(s.email, e.target.value as "viewer" | "editor")}
                  disabled={busy}
                  className="rounded-md border border-border bg-surface px-1.5 py-0.5 text-[12px] text-muted outline-none"
                >
                  <option value="viewer">只读</option>
                  <option value="editor">可编辑</option>
                </select>
                <button onClick={() => apply(s.email, "none")} disabled={busy} className="text-[12px] text-faint hover:text-accent" title="移除">
                  ✕
                </button>
              </div>
            ))}
          </div>
        )}
        <p className="mt-3 text-[11px] text-faint">只读用户可查看会话与产出文件;可编辑用户还能继续发送消息(在你的工作区中运行)。</p>
      </div>
    </div>
  );
}
