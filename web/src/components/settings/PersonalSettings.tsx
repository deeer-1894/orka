import { useEffect, useState } from "react";
import { api } from "../../api";
import type { Memory, Personalization } from "../../types";
import { toast } from "../../lib/toast";
import { Switch } from "../ui/switch";
import { Icon } from "../Icon";

const field = "w-full rounded-lg border border-border bg-bg px-3 py-2 text-base text-ink outline-none transition focus:border-accent/60";
const EMPTY: Personalization = { about_me: "", reply_style: "", auto_memory: false };

// PersonalSettings: what Orka should know about the user in every
// conversation, said once here instead of in each request.
export function PersonalSettings() {
  const [saved, setSaved] = useState<Personalization | null>(null);
  const [draft, setDraft] = useState(EMPTY);
  const [busy, setBusy] = useState(false);
  useEffect(() => { api.getPersonal().then(p => { const v = { ...EMPTY, ...p }; setSaved(v); setDraft(v); }).catch(() => setSaved(EMPTY)); }, []);
  const dirty = !!saved && (draft.about_me !== saved.about_me || draft.reply_style !== saved.reply_style);
  const save = async () => {
    setBusy(true);
    try { const p = await api.savePersonal(draft); setSaved(p); toast("已保存", "success"); }
    catch (e) { toast((e as Error).message || "保存失败", "error"); } finally { setBusy(false); }
  };
  if (!saved) return <p role="status" className="text-sm text-faint">正在读取…</p>;
  return <section aria-label="个性化" className="space-y-4">
    <div>
      <h2 className="text-lg font-medium text-ink">个性化</h2>
      <p className="mt-1 text-sm text-muted">这里写的内容会带到每一次对话里，不用每次都重新交代。</p>
    </div>
    <label className="block space-y-1">
      <span className="text-sm font-medium text-ink">关于我</span>
      <textarea value={draft.about_me} onChange={e => setDraft({ ...draft, about_me: e.target.value })} rows={4} maxLength={1500}
        placeholder="例如：我是一名产品经理，主要关注国内 AI 应用市场。" className={field + " resize-y"} />
    </label>
    <label className="block space-y-1">
      <span className="text-sm font-medium text-ink">回复偏好</span>
      <textarea value={draft.reply_style} onChange={e => setDraft({ ...draft, reply_style: e.target.value })} rows={4} maxLength={1500}
        placeholder="例如：中文回复，先给结论；对比类问题用表格；引用资料附链接。" className={field + " resize-y"} />
    </label>
    <div className="flex justify-end">
      <button onClick={save} disabled={!dirty || busy} className="rounded-lg bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40">{busy ? "保存中…" : "保存"}</button>
    </div>
  </section>;
}

// MemorySettings: whether runs may save what they learn about the user, and
// everything saved so far, each deletable for good.
export function MemorySettings() {
  const [personal, setPersonal] = useState<Personalization | null>(null);
  const [memories, setMemories] = useState<Memory[] | null>(null);
  const [query, setQuery] = useState("");
  useEffect(() => {
    api.getPersonal().then(p => setPersonal({ ...EMPTY, ...p })).catch(() => setPersonal(EMPTY));
    api.listMemories().then(m => setMemories(m || [])).catch(() => setMemories([]));
  }, []);
  const toggle = async (on: boolean) => {
    if (!personal) return;
    const next = { ...personal, auto_memory: on };
    setPersonal(next);
    try { await api.savePersonal(next); } catch (e) { setPersonal(personal); toast((e as Error).message || "保存失败", "error"); }
  };
  const remove = async (id: string) => {
    try { await api.deleteMemory(id); setMemories(ms => (ms || []).filter(m => m.memory_id !== id)); }
    catch (e) { toast((e as Error).message || "删除失败", "error"); }
  };
  const q = query.trim().toLowerCase();
  const shown = (memories || []).filter(m => !q || m.content.toLowerCase().includes(q));
  return <section aria-label="记忆" className="space-y-4">
    <div>
      <h2 className="text-lg font-medium text-ink">记忆</h2>
      <p className="mt-1 text-sm text-muted">开启后，Orka 会在对话中记下你提到的长期偏好和背景，并在之后的对话里参考。不会记录密码、证件号、银行卡号等敏感信息。</p>
    </div>
    <div className="flex items-center gap-3 rounded-xl border border-border px-4 py-3">
      <div className="min-w-0 flex-1">
        <div className="text-base text-ink">自动记忆</div>
        <div className="text-xs text-faint">关闭后既不新增，也不再使用已保存的记忆。</div>
      </div>
      {personal && <Switch checked={personal.auto_memory} onChange={v => void toggle(v)} label="自动记忆" />}
    </div>
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <h3 className="flex-1 text-sm font-medium text-ink">已保存 · {memories?.length ?? 0}</h3>
        {(memories?.length || 0) > 5 && <input value={query} onChange={e => setQuery(e.target.value)} placeholder="搜索记忆" aria-label="搜索记忆" className={field + " max-w-[200px] py-1 text-sm"} />}
      </div>
      {memories === null ? <p role="status" className="text-sm text-faint">正在读取…</p>
        : memories.length === 0 ? <p className="rounded-xl border border-dashed border-border px-4 py-6 text-center text-sm text-faint">还没有保存任何记忆。</p>
          : <ul className="divide-y divide-border rounded-xl border border-border">
            {shown.map(m => <li key={m.memory_id} className="flex items-start gap-2 px-4 py-2.5">
              <span className="min-w-0 flex-1 text-sm text-ink">{m.content}{m.project_id && <span className="ml-1.5 rounded bg-surface2 px-1.5 py-0.5 text-2xs text-faint">仅项目</span>}</span>
              <button onClick={() => void remove(m.memory_id)} aria-label={"删除记忆：" + m.content} title="删除" className="grid h-7 w-7 shrink-0 place-items-center rounded-md text-faint hover:bg-surface2 hover:text-accent"><Icon name="trash" size={14} /></button>
            </li>)}
          </ul>}
    </div>
  </section>;
}
