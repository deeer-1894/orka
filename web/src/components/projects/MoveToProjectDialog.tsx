import { useRef, useState } from "react";
import { api } from "../../api";
import type { Conversation, Project } from "../../types";
import { toastError } from "../../lib/toast";
import { useOverlay } from "../../lib/useOverlay";
import { Icon } from "../Icon";

// MoveToProjectDialog files a conversation under a project, or takes it out.
export function MoveToProjectDialog({ conv, projects, onClose, onMoved }: {
  conv: Conversation;
  projects: Project[];
  onClose: () => void;
  onMoved: (conversationID: string, projectID: string) => void;
}) {
  const [target, setTarget] = useState(conv.project_id || "");
  const [busy, setBusy] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);
  useOverlay(onClose, panelRef);
  const move = async () => {
    setBusy(true);
    try {
      await api.moveConversation(conv.conversation_id, target);
      onMoved(conv.conversation_id, target);
      onClose();
    } catch (e) { toastError((e as Error).message || "移动失败"); } finally { setBusy(false); }
  };
  const options = [{ id: "", name: "不属于任何项目" }, ...projects.map(p => ({ id: p.project_id, name: p.name }))];
  return <div className="overlay-in fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
    <div ref={panelRef} role="dialog" aria-modal="true" aria-label="移到项目" className="pop-in w-full max-w-sm rounded-2xl border border-border bg-surface p-4 shadow-xl" onClick={e => e.stopPropagation()}>
      <div className="mb-1 flex items-center gap-2">
        <Icon name="folder" size={15} className="text-muted" />
        <span className="flex-1 text-[15px] font-medium text-ink">移到项目</span>
        <button onClick={onClose} aria-label="关闭" title="关闭" className="grid h-7 w-7 place-items-center rounded-lg text-faint hover:bg-surface2 hover:text-ink"><Icon name="close" size={15} /></button>
      </div>
      <p className="mb-3 truncate text-[12px] text-faint">{conv.title}</p>
      {projects.length === 0 && <p className="mb-2 text-[12.5px] text-muted">还没有项目。先在侧边栏的“项目”里新建一个。</p>}
      <div role="radiogroup" aria-label="目标项目" className="max-h-64 space-y-1 overflow-y-auto">
        {options.map(o => <label key={o.id || "none"} className={"flex cursor-pointer items-center gap-2 rounded-lg px-2.5 py-2 text-[13.5px] " + (target === o.id ? "bg-accentsoft text-ink" : "text-muted hover:bg-surface2")}>
          <input type="radio" name="project" checked={target === o.id} onChange={() => setTarget(o.id)} className="accent-[var(--accent,#b5532f)]" />
          {o.name}
        </label>)}
      </div>
      <div className="mt-3 flex justify-end gap-2">
        <button onClick={onClose} className="rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted hover:text-ink">取消</button>
        <button onClick={move} disabled={busy || target === (conv.project_id || "")} className="rounded-lg bg-accent px-3 py-1.5 text-[13px] text-white hover:opacity-90 disabled:opacity-40">移动</button>
      </div>
    </div>
  </div>;
}
