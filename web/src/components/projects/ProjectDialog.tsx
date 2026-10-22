import { useRef, useState } from "react";
import type { Project } from "../../types";
import { useOverlay } from "../../lib/useOverlay";
import { Icon } from "../Icon";

// ProjectDialog names a new project or renames an existing one.
export function ProjectDialog({ project, onClose, onSubmit }: {
  project?: Project; // absent: create
  onClose: () => void;
  onSubmit: (v: { name: string; description: string }) => Promise<void>;
}) {
  const [name, setName] = useState(project?.name || "");
  const [description, setDescription] = useState(project?.description || "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const panelRef = useRef<HTMLDivElement>(null);
  useOverlay(onClose, panelRef);
  const title = project ? "重命名项目" : "新建项目";
  const submit = async () => {
    if (!name.trim()) return;
    setBusy(true); setError("");
    try { await onSubmit({ name: name.trim(), description: description.trim() }); onClose(); }
    catch (e) { setError((e as Error).message || "保存失败"); } finally { setBusy(false); }
  };
  const field = "w-full rounded-lg border border-border bg-bg px-3 py-2 text-sm text-ink outline-none focus:border-accent/50";
  return <div className="overlay-in fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
    <div ref={panelRef} role="dialog" aria-modal="true" aria-label={title} className="pop-in w-full max-w-sm rounded-2xl border border-border bg-surface p-4 shadow-xl" onClick={e => e.stopPropagation()}>
      <div className="mb-3 flex items-center gap-2">
        <Icon name="folder" size={15} className="text-muted" />
        <span className="flex-1 text-md font-medium text-ink">{title}</span>
        <button onClick={onClose} aria-label="关闭" title="关闭" className="grid h-7 w-7 place-items-center rounded-lg text-faint hover:bg-surface2 hover:text-ink"><Icon name="close" size={15} /></button>
      </div>
      <form className="space-y-3" onSubmit={e => { e.preventDefault(); void submit(); }}>
        <label className="block space-y-1">
          <span className="text-sm font-medium text-ink">名称</span>
          <input autoFocus value={name} onChange={e => setName(e.target.value)} maxLength={60} placeholder="例如：竞品研究" className={field} />
        </label>
        <label className="block space-y-1">
          <span className="text-sm font-medium text-ink">说明（可选）</span>
          <input value={description} onChange={e => setDescription(e.target.value)} maxLength={300} placeholder="这个项目在做什么" className={field} />
        </label>
        {error && <p role="alert" className="text-xs text-accent">{error}</p>}
        <div className="flex justify-end gap-2 pt-1">
          <button type="button" onClick={onClose} className="rounded-lg border border-border px-3 py-1.5 text-sm text-muted hover:text-ink">取消</button>
          <button type="submit" disabled={busy || !name.trim()} className="rounded-lg bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40">{project ? "保存" : "创建"}</button>
        </div>
      </form>
    </div>
  </div>;
}
