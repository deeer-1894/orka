import { useEffect, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { api, type ConnectorInput, type SystemStatus } from "../../api";
import type { Connector } from "../../types";
import { toast } from "../../lib/toast";
import { Button } from "../ui/button";
import { Icon, type IconName } from "../Icon";
import { MemorySettings, PersonalSettings } from "./PersonalSettings";
import { SkillSettings } from "./SkillSettings";

export type Section = "personal" | "memory" | "skills" | "connectors" | "about";
const SECTIONS: { id: Section; label: string; icon: IconName }[] = [
  { id: "personal", label: "个性化", icon: "users" },
  { id: "memory", label: "记忆", icon: "sparkle" },
  { id: "skills", label: "技能", icon: "book" },
  { id: "connectors", label: "连接器", icon: "plug" },
  { id: "about", label: "关于", icon: "gear" },
];
const TRANSPORTS: { id: string; label: string }[] = [
  { id: "streamable_http", label: "Streamable HTTP" },
  { id: "http", label: "SSE" },
];
const field = "w-full rounded-lg border border-border bg-bg px-3 py-2 text-[14px] text-ink outline-none transition focus:border-accent/60";

// SettingsDialog holds account-level configuration that is not part of a
// conversation: personalization, memory, external tool connectors and
// service information.
export function SettingsDialog({ onClose, initial = "personal" }: { onClose: () => void; initial?: Section }) {
  const [section, setSection] = useState<Section>(initial);
  return <Dialog.Root open onOpenChange={open => { if (!open) onClose(); }}>
    <Dialog.Portal>
      <Dialog.Overlay className="overlay-in fixed inset-0 z-50 bg-black/40" />
      <Dialog.Content className="pop-in fixed left-1/2 top-1/2 z-50 flex h-[min(620px,calc(100vh-32px))] w-[min(820px,calc(100vw-24px))] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-2xl border border-border bg-surface shadow-xl sm:flex-row">
        <nav aria-label="设置分区" className="flex shrink-0 gap-1 border-b border-border bg-bg/40 p-3 sm:w-48 sm:flex-col sm:border-b-0 sm:border-r">
          <Dialog.Title className="hidden px-2 pb-3 pt-1 font-serif text-[18px] text-ink sm:block">设置</Dialog.Title>
          {SECTIONS.map(s => <button key={s.id} onClick={() => setSection(s.id)} aria-current={section === s.id ? "page" : undefined}
            className={"flex items-center gap-2 rounded-lg px-3 py-2 text-[14px] transition " + (section === s.id ? "bg-surface2 text-ink" : "text-muted hover:bg-surface2/60")}>
            <Icon name={s.icon} size={15} />{s.label}</button>)}
        </nav>
        <div className="relative min-h-0 flex-1 overflow-y-auto px-6 py-5">
          <Dialog.Close asChild><Button variant="ghost" size="icon" aria-label="关闭设置" className="absolute right-3 top-3"><Icon name="close" size={16} /></Button></Dialog.Close>
          <Dialog.Description className="sr-only">个性化、记忆、连接器和服务信息</Dialog.Description>
          {section === "personal" ? <PersonalSettings /> : section === "memory" ? <MemorySettings /> : section === "skills" ? <SkillSettings /> : section === "connectors" ? <Connectors /> : <About />}
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}

function parseHeaders(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const i = line.indexOf(":");
    if (i > 0) out[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  return out;
}

function Connectors() {
  const [list, setList] = useState<Connector[] | null>(null);
  const [adding, setAdding] = useState(false);
  const [form, setForm] = useState({ name: "", transport: "streamable_http", url: "", headers: "" });
  const [probe, setProbe] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState<"" | "test" | "save">("");
  // Removal is confirmed inline: a second modal cannot take focus inside this one.
  const [confirming, setConfirming] = useState("");
  const refresh = () => api.listConnectors().then(r => setList(r.connectors || [])).catch(() => setList([]));
  useEffect(() => { void refresh(); }, []);
  const input = (): ConnectorInput => ({ name: form.name.trim(), transport: form.transport, url: form.url.trim(), headers: parseHeaders(form.headers) });
  const valid = form.name.trim() && /^https?:\/\//.test(form.url.trim());

  const test = async () => {
    setBusy("test"); setProbe(null);
    try {
      const r = await api.testConnector(input());
      setProbe(r.ok ? { ok: true, text: `连接成功，发现 ${r.tools?.length || 0} 个工具${r.tools?.length ? "：" + r.tools.slice(0, 6).join("、") + (r.tools.length > 6 ? " 等" : "") : ""}` } : { ok: false, text: r.error || "连接失败" });
    } catch (e) { setProbe({ ok: false, text: (e as Error).message || "连接失败" }); } finally { setBusy(""); }
  };
  const save = async () => {
    setBusy("save");
    try {
      await api.createConnector(input());
      setForm({ name: "", transport: "streamable_http", url: "", headers: "" }); setProbe(null); setAdding(false);
      toast("已添加连接器，新任务即可使用它的工具", "success");
      void refresh();
    } catch (e) { setProbe({ ok: false, text: (e as Error).message || "添加失败" }); } finally { setBusy(""); }
  };
  const remove = async (c: Connector) => {
    setConfirming("");
    try { await api.deleteConnector(c.connector_id); toast("已移除，之后的运行不再使用它的工具", "success"); void refresh(); } catch { /* reported */ }
  };

  return <section aria-label="连接器">
    <h2 className="font-serif text-[20px] text-ink">连接器</h2>
    <p className="mt-1 max-w-lg text-[13px] text-muted">接入 MCP 服务后，它提供的工具会和内置工具一样，由 Orka 在需要时自动调用。</p>
    <div className="mt-5 flex items-center justify-between">
      <span className="text-[12px] text-faint">{list ? `${list.length} 个连接器` : "正在读取…"}</span>
      {!adding && <Button size="sm" onClick={() => setAdding(true)}><Icon name="plus" size={14} />添加连接器</Button>}
    </div>
    {adding && <form className="mt-3 flex flex-col gap-3 rounded-xl border border-border bg-bg/40 p-4" onSubmit={e => { e.preventDefault(); if (valid) void save(); }}>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1.5 text-[13px] font-medium text-ink">名称<input className={field} value={form.name} placeholder="例如：GitHub" onChange={e => setForm(f => ({ ...f, name: e.target.value }))} /></label>
        <label className="flex flex-col gap-1.5 text-[13px] font-medium text-ink">传输方式<select className={field} value={form.transport} onChange={e => setForm(f => ({ ...f, transport: e.target.value }))}>{TRANSPORTS.map(t => <option key={t.id} value={t.id}>{t.label}</option>)}</select></label>
      </div>
      <label className="flex flex-col gap-1.5 text-[13px] font-medium text-ink">服务地址<input className={field} inputMode="url" value={form.url} placeholder="https://mcp.example.com/mcp" onChange={e => setForm(f => ({ ...f, url: e.target.value }))} /></label>
      <label className="flex flex-col gap-1.5 text-[13px] font-medium text-ink">请求头（可选）
        <textarea className={field + " min-h-[64px] font-mono text-[12px]"} value={form.headers} placeholder={"每行一个，例如\nAuthorization: Bearer <token>"} onChange={e => setForm(f => ({ ...f, headers: e.target.value }))} />
        <span className="text-[12px] font-normal text-faint">保存后列表中不再显示请求头的值。</span>
      </label>
      {probe && <p role={probe.ok ? "status" : "alert"} className={"rounded-lg px-3 py-2 text-[13px] " + (probe.ok ? "bg-surface2 text-ok" : "bg-accentsoft text-accent")}>{probe.text}</p>}
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" variant="outline" size="sm" disabled={!valid || !!busy} onClick={() => void test()}>{busy === "test" ? "测试中…" : "测试连接"}</Button>
        <div className="ml-auto flex gap-2">
          <Button type="button" variant="ghost" size="sm" disabled={!!busy} onClick={() => { setAdding(false); setProbe(null); }}>取消</Button>
          <Button type="submit" size="sm" disabled={!valid || !!busy}>{busy === "save" ? "添加中…" : "添加"}</Button>
        </div>
      </div>
    </form>}
    {list && list.length === 0 && !adding && <div className="mt-4 rounded-xl border border-dashed border-border px-6 py-10 text-center">
      <Icon name="plug" size={22} className="mx-auto text-faint" />
      <p className="mt-3 text-[14px] text-muted">还没有连接器</p>
      <p className="mt-1 text-[13px] text-faint">例如接入 GitHub、Notion 或公司内部的 MCP 服务。</p>
    </div>}
    <ul className="mt-3 flex flex-col gap-2">{(list || []).map(c => <li key={c.connector_id} className="flex items-center gap-3 rounded-xl border border-border px-4 py-3">
      <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-surface2 text-muted"><Icon name="plug" size={16} /></span>
      <div className="min-w-0 flex-1">
        <div className="truncate text-[14px] text-ink">{c.name}</div>
        <div className="truncate text-[12px] text-faint">{TRANSPORTS.find(t => t.id === c.transport)?.label || c.transport} · {c.url}</div>
      </div>
      {confirming === c.connector_id
        ? <div className="flex shrink-0 gap-1"><Button variant="ghost" size="sm" onClick={() => setConfirming("")}>取消</Button><Button size="sm" onClick={() => void remove(c)}>确认移除</Button></div>
        : <Button variant="ghost" size="icon" aria-label={`移除 ${c.name}`} onClick={() => setConfirming(c.connector_id)}><Icon name="trash" size={15} /></Button>}
    </li>)}</ul>
  </section>;
}

function About() {
  const [data, setData] = useState<SystemStatus | null>(null);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    let alive = true;
    setError("");
    api.systemStatus().then(s => { if (alive) setData(s); }).catch(e => { if (alive) setError((e as Error).message); });
    return () => { alive = false; };
  }, [revision]);
  return <section aria-label="关于">
    <h2 className="font-serif text-[20px] text-ink">关于</h2>
    {error && <p role="alert" className="mt-4 text-[13px] text-accent">服务信息暂不可用：{error}</p>}
    {!data && !error && <p role="status" className="mt-4 text-[13px] text-muted">正在读取…</p>}
    {data && <>
      <dl className="mt-4 grid grid-cols-[88px_1fr] gap-y-2 text-[13px]">
        <dt className="text-faint">版本</dt><dd className="break-all text-ink">{data.version}{data.modified ? "（含未提交修改）" : ""}</dd>
        <dt className="text-faint">构建时间</dt><dd className="text-ink">{data.build_time || "未知"}</dd>
        <dt className="text-faint">启动时间</dt><dd className="text-ink">{data.started_at ? new Date(data.started_at).toLocaleString() : "未知"}</dd>
      </dl>
      <div className="mt-6 flex items-center justify-between">
        <h3 className="text-[14px] font-medium text-ink">服务状态</h3>
        <Button variant="ghost" size="sm" onClick={() => setRevision(n => n + 1)}><Icon name="refresh" size={14} />刷新</Button>
      </div>
      <p className={"mt-1 text-[13px] " + (data.ready ? "text-ok" : "text-accent")}>{data.ready ? "所有服务已就绪" : "部分服务尚未就绪"}</p>
      <ul className="mt-3 flex flex-col gap-2">{data.services.map(s => <li key={s.name} className="flex items-start gap-3 rounded-xl border border-border px-4 py-3">
        <span className={"mt-1.5 h-2 w-2 shrink-0 rounded-full " + (s.status === "ready" || s.status === "ok" ? "bg-ok" : "bg-accent")} />
        <div className="min-w-0"><div className="text-[14px] text-ink">{s.name} <span className="text-[12px] text-faint">{s.status}</span></div>{s.detail && <p className="mt-0.5 break-words text-[12px] text-faint">{s.detail}</p>}</div>
      </li>)}</ul>
    </>}
  </section>;
}
