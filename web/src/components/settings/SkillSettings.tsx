import { useEffect, useState } from "react";
import { api, type SkillSummary } from "../../api";
import { toast } from "../../lib/toast";
import { Switch } from "../ui/switch";
import { Icon } from "../Icon";

const field = "w-full rounded-lg border border-border bg-bg px-3 py-2 text-[14px] text-ink outline-none transition focus:border-accent/60";

// SkillSettings manages the skills a user can pick in the composer: switch
// any off, read what each tells the model, install from a SKILL.md link
// after previewing it, delete installed ones.
export function SkillSettings() {
  const [skills, setSkills] = useState<SkillSummary[] | null>(null);
  const [open, setOpen] = useState("");
  const [prompts, setPrompts] = useState<Record<string, string>>({});
  const [confirming, setConfirming] = useState("");
  const [url, setUrl] = useState("");
  const [preview, setPreview] = useState<{ name: string; description: string; prompt: string } | null>(null);
  const [busy, setBusy] = useState<"" | "preview" | "install">("");
  const load = () => api.listSkills().then(r => setSkills(r.skills || [])).catch(() => setSkills([]));
  useEffect(() => { void load(); }, []);

  const toggleOpen = async (name: string) => {
    setOpen(o => (o === name ? "" : name));
    if (prompts[name] === undefined) {
      try { const r = await api.getSkill(name); setPrompts(p => ({ ...p, [name]: r.prompt || "（空）" })); }
      catch { setPrompts(p => ({ ...p, [name]: "（读取失败）" })); }
    }
  };
  const setEnabled = async (name: string, enabled: boolean) => {
    setSkills(list => (list || []).map(s => (s.name === name ? { ...s, enabled } : s)));
    try { await api.enableSkill(name, enabled); } catch (e) { toast((e as Error).message || "操作失败", "error"); void load(); }
  };
  const remove = async (name: string) => {
    try { await api.deleteSkill(name); toast(`已删除技能“${name}”`, "success"); setConfirming(""); void load(); }
    catch (e) { toast((e as Error).message || "删除失败", "error"); }
  };
  const fetchPreview = async () => {
    setBusy("preview"); setPreview(null);
    try { setPreview(await api.previewSkill(url.trim())); }
    catch (e) { toast((e as Error).message || "无法读取这个链接", "error"); } finally { setBusy(""); }
  };
  const install = async () => {
    setBusy("install");
    try { const r = await api.installSkill(url.trim()); toast(`已安装技能“${r.name}”`, "success"); setUrl(""); setPreview(null); void load(); }
    catch (e) { toast((e as Error).message || "安装失败", "error"); } finally { setBusy(""); }
  };

  const group = (title: string, list: SkillSummary[]) => list.length > 0 && <div className="space-y-2">
    <h3 className="text-[13px] font-medium text-ink">{title} · {list.length}</h3>
    <ul className="divide-y divide-border rounded-xl border border-border">
      {list.map(s => <li key={s.name} className="px-4 py-2.5">
        <div className="flex items-center gap-2">
          <button onClick={() => void toggleOpen(s.name)} aria-expanded={open === s.name} className="flex min-w-0 flex-1 items-center gap-2 text-left">
            <Icon name="chevron" size={12} className={"shrink-0 text-faint transition-transform " + (open === s.name ? "" : "-rotate-90")} />
            <span className="min-w-0">
              <span className={"block text-[14px] " + (s.enabled === false ? "text-faint" : "text-ink")}>{s.name}</span>
              <span className="block truncate text-[12px] text-faint">{s.description}</span>
            </span>
          </button>
          {!s.builtin && (confirming === s.name
            ? <span className="flex shrink-0 items-center gap-1 text-[12px]"><button onClick={() => void remove(s.name)} className="text-accent hover:underline">确认删除</button><button onClick={() => setConfirming("")} className="text-faint hover:text-ink">取消</button></span>
            : <button onClick={() => setConfirming(s.name)} aria-label={"删除技能 " + s.name} title="删除" className="grid h-7 w-7 shrink-0 place-items-center rounded-md text-faint hover:bg-surface2 hover:text-accent"><Icon name="trash" size={14} /></button>)}
          <Switch checked={s.enabled !== false} onChange={v => void setEnabled(s.name, v)} label={(s.enabled === false ? "启用" : "停用") + "技能 " + s.name} />
        </div>
        {open === s.name && <pre className="mt-2 max-h-48 overflow-y-auto whitespace-pre-wrap rounded-lg bg-surface2/60 px-3 py-2 text-[12px] leading-relaxed text-muted">{prompts[s.name] ?? "读取中…"}</pre>}
      </li>)}
    </ul>
  </div>;

  return <section aria-label="技能" className="space-y-4">
    <div>
      <h2 className="text-[16px] font-medium text-ink">技能</h2>
      <p className="mt-1 text-[13px] text-muted">技能是给模型的专长说明。停用的技能不会出现在输入框的技能菜单里，模型也不会自行选用。</p>
    </div>
    {skills === null ? <p role="status" className="text-[13px] text-faint">正在读取…</p> : <>
      {group("内置", skills.filter(s => s.builtin))}
      {group("已安装", skills.filter(s => !s.builtin))}
    </>}
    <div className="space-y-2 rounded-xl border border-border p-4">
      <h3 className="text-[13px] font-medium text-ink">从链接安装</h3>
      <div className="flex gap-2">
        <input value={url} onChange={e => { setUrl(e.target.value); setPreview(null); }} placeholder="https://…/SKILL.md" aria-label="SKILL.md 链接" className={field} />
        <button onClick={() => void fetchPreview()} disabled={!url.trim() || busy !== ""} className="shrink-0 rounded-lg border border-border px-3 text-[13px] text-muted hover:text-ink disabled:opacity-40">{busy === "preview" ? "读取中…" : "预览"}</button>
      </div>
      {preview && <div aria-label="技能预览" className="space-y-2 rounded-lg bg-surface2/50 p-3">
        <div className="text-[14px] text-ink">{preview.name}<span className="ml-2 text-[12px] text-faint">{preview.description}</span></div>
        <pre className="max-h-40 overflow-y-auto whitespace-pre-wrap text-[12px] leading-relaxed text-muted">{preview.prompt}</pre>
        <div className="flex justify-end"><button onClick={() => void install()} disabled={busy !== ""} className="rounded-lg bg-accent px-3 py-1.5 text-[13px] text-white hover:opacity-90 disabled:opacity-40">{busy === "install" ? "安装中…" : "安装"}</button></div>
      </div>}
    </div>
  </section>;
}
