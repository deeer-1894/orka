import { useEffect, useState } from "react";
import { api, projectScope } from "../../api";
import type { Conversation, Project } from "../../types";
import { Icon } from "../Icon";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { FilesPanel } from "../FilesPanel";
import { toast, toastError } from "../../lib/toast";
import { COPY } from "../../lib/copy";

// ProjectPage is one project's home: what it is, the standing instructions
// every run in it follows, its shared files, and its conversations.
export function ProjectPage({ project, conversations, email, onSaved, onRename, onDelete, onNewConversation, onOpenConversation, onToggleSidebar }: {
  project: Project;
  conversations: Conversation[];
  email: string;
  onSaved: (p: Project) => void;
  onRename: () => void;
  onDelete: () => void;
  onNewConversation: () => void;
  onOpenConversation: (id: string) => void;
  onToggleSidebar: () => void;
}) {
  const [draft, setDraft] = useState(project);
  const [skills, setSkills] = useState<{ name: string; description: string }[]>([]);
  const [saving, setSaving] = useState(false);
  useEffect(() => setDraft(project), [project]);
  useEffect(() => { api.listSkills().then(r => setSkills(r.skills || [])).catch(() => {}); }, []);
  const dirty = draft.name !== project.name || (draft.description || "") !== (project.description || "")
    || (draft.instructions || "") !== (project.instructions || "") || (draft.default_skill || "") !== (project.default_skill || "");
  const save = async () => {
    setSaving(true);
    try {
      const saved = await api.saveProject({ project_id: project.project_id, name: draft.name, description: draft.description, instructions: draft.instructions, default_skill: draft.default_skill });
      onSaved(saved);
      toast("项目已保存", "success");
    } catch (e) { toastError((e as Error).message || "保存失败"); } finally { setSaving(false); }
  };
  return <main aria-label={"项目 " + project.name} className="relative flex min-w-0 flex-1 flex-col overflow-y-auto">
    <header className="flex h-14 shrink-0 items-center gap-2 px-2 sm:px-4">
      <button onClick={onToggleSidebar} className="grid h-8 w-8 place-items-center rounded-lg text-muted hover:bg-surface2" title={COPY.sidebarHint} aria-label={COPY.toggleSidebar}><Icon name="panel" size={18} /></button>
      <Icon name="folder" size={16} className="text-muted" />
      <span className="min-w-0 flex-1 truncate text-md text-ink">{project.name}</span>
      <button onClick={onNewConversation} className="inline-flex items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90"><Icon name="plus" size={14} />{COPY.newChat}</button>
      <DropdownMenu>
        <DropdownMenuTrigger aria-label="项目操作" title="项目操作" className="grid h-8 w-8 place-items-center rounded-lg text-muted outline-none hover:bg-surface2"><Icon name="more" size={18} /></DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-40">
          <DropdownMenuItem onSelect={onRename}><Icon name="rename" size={14} className="text-muted" />重命名</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={onDelete} className="text-accent"><Icon name="trash" size={14} />删除</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </header>
    <div className="mx-auto w-full max-w-3xl space-y-6 px-4 pb-16 pt-2 sm:px-6">
      <section aria-label="项目设置" className="space-y-3 rounded-2xl border border-border bg-surface p-4">
        <label className="block space-y-1">
          <span className="text-sm font-medium text-ink">名称</span>
          <input value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })} maxLength={60}
            className="w-full rounded-lg border border-border bg-bg px-3 py-2 text-base outline-none focus:border-accent/50" />
        </label>
        <label className="block space-y-1">
          <span className="text-sm font-medium text-ink">说明</span>
          <input value={draft.description || ""} onChange={e => setDraft({ ...draft, description: e.target.value })} maxLength={300} placeholder="这个项目在做什么"
            className="w-full rounded-lg border border-border bg-bg px-3 py-2 text-base outline-none focus:border-accent/50" />
        </label>
        <label className="block space-y-1">
          <span className="text-sm font-medium text-ink">项目指令</span>
          <textarea value={draft.instructions || ""} onChange={e => setDraft({ ...draft, instructions: e.target.value })} maxLength={8000} rows={5}
            placeholder="项目里每次运行都会遵守，例如：关注国内 AI 智能体产品；结论用表格对比并附来源链接；中文输出。"
            className="w-full resize-y rounded-lg border border-border bg-bg px-3 py-2 text-sm leading-relaxed outline-none focus:border-accent/50" />
          <span className="block text-xs text-faint">{(draft.instructions || "").length}/8000</span>
        </label>
        <label className="block space-y-1">
          <span className="text-sm font-medium text-ink">默认技能</span>
          <select value={draft.default_skill || ""} onChange={e => setDraft({ ...draft, default_skill: e.target.value })}
            className="w-full rounded-lg border border-border bg-bg px-3 py-2 text-base outline-none focus:border-accent/50">
            <option value="">不使用</option>
            {skills.map(s => <option key={s.name} value={s.name}>{s.name}</option>)}
          </select>
          <span className="block text-xs text-faint">项目里的对话没有选择技能时使用它。</span>
        </label>
        <div className="flex items-center gap-2">
          <span className="flex-1" />
          <button onClick={save} disabled={!dirty || saving || !draft.name.trim()} className="rounded-lg bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40">{saving ? "保存中…" : "保存"}</button>
        </div>
      </section>

      <section aria-label="项目对话" className="space-y-2">
        <h2 className="text-sm font-medium text-ink">对话 · {conversations.length}</h2>
        {conversations.length === 0
          ? <p className="rounded-xl border border-dashed border-border px-4 py-6 text-center text-sm text-faint">还没有对话。点右上角的“新对话”，在这个项目里开始工作。</p>
          : <ul className="divide-y divide-border overflow-hidden rounded-xl border border-border bg-surface">
            {conversations.map(c => <li key={c.conversation_id}>
              <button onClick={() => onOpenConversation(c.conversation_id)} className="flex w-full items-center gap-2 px-4 py-2.5 text-left text-base text-ink hover:bg-surface2">
                <Icon name="chat" size={14} className="text-muted" /><span className="min-w-0 flex-1 truncate">{c.title}</span>
                <span className="shrink-0 text-xs text-faint">{new Date(c.created_at).toLocaleDateString()}</span>
              </button>
            </li>)}
          </ul>}
      </section>

      <section aria-label="项目文件" className="space-y-2">
        <h2 className="text-sm font-medium text-ink">共享文件</h2>
        <p className="text-xs text-faint">项目里的每个对话都能读取这些文件（在工作区的 project/ 目录）。运行写到 project/ 的文件会保存回这里，并保留旧版本。</p>
        <div className="rounded-xl border border-border bg-surface">
          <FilesPanel email={email} conversationID={projectScope(project.project_id)} />
        </div>
      </section>
    </div>
  </main>;
}
