import { useEffect, useState, type ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { modelSettings, type ModelSettingsData, type ModelSettingsInput } from "../api";
import { exportModelProfile, importModelProfile } from "../lib/modelSettings";
import { Button } from "./ui/button";

const PROVIDERS = [
  { id: "custom", label: "自定义 / OpenAI 兼容", url: "" },
  { id: "openai", label: "OpenAI", url: "https://api.openai.com/v1" },
  { id: "deepseek", label: "DeepSeek", url: "https://api.deepseek.com/v1" },
  { id: "openrouter", label: "OpenRouter", url: "https://openrouter.ai/api/v1" },
  { id: "ollama", label: "Ollama（本地）", url: "http://localhost:11434/v1" },
  { id: "qwen", label: "通义千问 / 百炼", url: "https://dashscope.aliyuncs.com/compatible-mode/v1" },
  { id: "siliconflow", label: "硅基流动", url: "https://api.siliconflow.cn/v1" },
];
const EMPTY: ModelSettingsData = { provider: "custom", base_url: "", api_key_set: false, models: [], enabled: false };
const inputStyle = "w-full rounded-lg border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-accent";

export function ModelSettings({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const [config, setConfig] = useState<ModelSettingsData>(EMPTY);
  const [key, setKey] = useState("");
  const [savedBase, setSavedBase] = useState("");
  const [modelText, setModelText] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<"save" | "discover" | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [profileText, setProfileText] = useState("");
  useEffect(() => {
    let alive = true;
    modelSettings.get().then(c => {
      if (alive) { setConfig({ ...c, provider: (!c.provider || c.provider === "openai-compatible") ? "custom" : c.provider }); setSavedBase(c.base_url); setModelText((c.models || []).join("\n")); }
    }).catch(e => { if (alive) setError(e.message); }).finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, []);
  const patch = (p: Partial<ModelSettingsData>) => { setConfig(c => ({ ...c, ...p })); setNotice(""); };
  const payload = (): ModelSettingsInput => ({
    provider: config.provider, base_url: config.base_url.trim(), enabled: config.enabled,
    models: [...new Set(modelText.split(/[\n,]/).map(m => m.trim()).filter(Boolean))],
    ...(key ? { api_key: key } : {}),
  });
  const discover = async () => {
    setBusy("discover"); setError(""); setNotice("");
    try {
      const r = await modelSettings.discover({ provider: config.provider, base_url: config.base_url.trim(), ...(key ? { api_key: key } : {}) });
      setModelText([...new Set([...modelText.split(/[\n,]/).map(m => m.trim()).filter(Boolean), ...r.models])].join("\n"));
      setNotice(r.models.length ? `获取到 ${r.models.length} 个模型，可在下方选择或手动填写。` : "该服务未返回模型，可以直接手动填写模型名称。");
    } catch (e) { setError(`获取模型失败：${(e as Error).message}。可以手动填写模型名称后保存。`); }
    finally { setBusy(null); }
  };
  const save = async () => {
    setBusy("save"); setError(""); setNotice("");
    try {
      const saved = await modelSettings.save(payload());
      setConfig(saved); setSavedBase(saved.base_url); setKey(""); setModelText((saved.models || []).join("\n"));
      setNotice("配置已保存，对下一次发送生效。"); onSaved();
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(null); }
  };
  return <Dialog.Root open onOpenChange={open => { if (!open && !busy) onClose(); }}>
    <Dialog.Portal>
      <Dialog.Overlay className="fixed inset-0 z-50 bg-black/30" />
      <Dialog.Content className="fixed left-1/2 top-1/2 z-50 max-h-[90vh] w-[min(640px,calc(100vw-24px))] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-2xl border border-border bg-surface p-6 shadow-xl">
        <div className="flex items-center justify-between gap-4">
          <Dialog.Title className="font-serif text-xl text-ink">模型配置</Dialog.Title>
          <Dialog.Close asChild><Button variant="ghost" size="sm" disabled={!!busy} aria-label="关闭模型配置">关闭</Button></Dialog.Close>
        </div>
        <Dialog.Description className="mt-2 text-sm text-muted">配置你的模型服务。支持 OpenAI 兼容接口，也可手动填写模型名称。</Dialog.Description>
        {loading ? <p className="py-8 text-muted">正在读取配置…</p> : <form className="mt-5 flex flex-col gap-4" onSubmit={e => { e.preventDefault(); void save(); }}>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={config.enabled} onChange={e => patch({ enabled: e.target.checked })} disabled={!!busy} />使用我的模型配置（关闭时使用系统默认）</label>
          <fieldset disabled={!!busy} className="flex min-w-0 flex-col gap-4">
            <SettingField label="厂商" id="model-provider"><select id="model-provider" className={inputStyle} value={config.provider} onChange={e => {
              const preset = PROVIDERS.find(p => p.id === e.target.value)!;
              patch({ provider: preset.id, base_url: preset.url, enabled: true }); setKey(""); setModelText("");
            }}>{PROVIDERS.map(p => <option key={p.id} value={p.id}>{p.label}</option>)}{!PROVIDERS.some(p => p.id === config.provider) && <option value={config.provider}>{config.provider}</option>}</select></SettingField>
            <SettingField label="Base URL" id="model-base"><input id="model-base" className={inputStyle} type="url" required={config.enabled} value={config.base_url} placeholder="https://your-provider.example/v1" onChange={e => patch({ base_url: e.target.value })} /></SettingField>
            <SettingField label="API Key" id="model-key"><input id="model-key" className={inputStyle} type="password" autoComplete="new-password" value={key} placeholder={config.api_key_set && savedBase === config.base_url ? "已保存，留空保持现有密钥" : "输入 API Key；本地服务可留空"} onChange={e => setKey(e.target.value)} /></SettingField>
            {config.api_key_set && savedBase !== config.base_url && <p className="text-xs text-muted">地址已改变，请输入新服务的密钥。不会向新地址发送旧密钥。</p>}
            <div><Button type="button" variant="outline" disabled={!config.base_url.trim() || !!busy} onClick={() => void discover()}>{busy === "discover" ? "正在获取模型…" : "获取模型列表"}</Button></div>
            <SettingField label="可选模型列表（每行一个，也支持逗号分隔）" id="model-list"><textarea id="model-list" className={inputStyle} required={config.enabled} rows={4} value={modelText} onChange={e => setModelText(e.target.value)} /></SettingField>
            <p className="text-xs text-muted">Auto 使用列表中的第一个模型；也可以在顶部选择指定模型。调整列表顺序即可更改 Auto 默认模型。</p>
            <details className="rounded-lg border border-border p-3">
              <summary className="cursor-pointer text-sm text-muted">配置文件 · 导入 / 导出</summary>
              <p className="my-2 text-xs text-faint">JSON 格式。导出不包含 API Key；导入后需要保存才会生效。</p>
              <textarea aria-label="JSON 配置" rows={7} className={inputStyle + " font-mono text-xs"} value={profileText} onChange={e => setProfileText(e.target.value)} />
              <div className="mt-2 flex gap-2">
                <Button type="button" variant="outline" onClick={() => {
                  const json = exportModelProfile(payload()); setProfileText(json);
                  const url = URL.createObjectURL(new Blob([json], { type: "application/json" }));
                  const link = document.createElement("a"); link.href = url; link.download = "orka-model.json"; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
                }}>导出配置</Button>
                <Button type="button" variant="outline" onClick={() => {
                  try { const p = importModelProfile(profileText); setConfig({ ...p, api_key_set: config.api_key_set }); setKey(""); setModelText(p.models.join("\n")); setError(""); setNotice("配置已导入表单，请检查后保存。"); } catch (e) { setError((e as Error).message); }
                }}>应用 JSON</Button>
              </div>
            </details>
          </fieldset>
          {notice && <p role="status" className="text-sm text-muted">{notice}</p>}
          <div className="flex justify-end"><Button type="submit" disabled={!!busy}>{busy === "save" ? "保存中…" : "保存配置"}</Button></div>
        </form>}
        {error && <p role="alert" className="mt-3 text-sm text-accent">{error}</p>}
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
function SettingField({ label, id, children }: { label: string; id: string; children: ReactNode }) {
  return <div className="flex flex-col gap-1.5"><label htmlFor={id} className="text-sm text-muted">{label}</label>{children}</div>;
}
