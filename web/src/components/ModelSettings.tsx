import { ActionChip } from './ActionChip';
import { lazy, Suspense, useEffect, useState, type ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { modelSettings, settings as settingsApi, type ModelSettingsData, type ModelSettingsInput, type SettingsView } from "../api";
import { Icon } from "./Icon";
import { toast, toastError } from "../lib/toast";
import { exportModelProfile, importModelProfile } from "../lib/modelSettings";
import { Button } from "./ui/button";

const ModelProfilesDialog = lazy(() => import("./ModelProfilesDialog"));
const PROVIDERS = [
  { id: "custom", label: "自定义 / OpenAI 兼容", url: "" },
  { id: "openai", label: "OpenAI", url: "https://api.openai.com/v1" },
  { id: "volcengine-plan", label: "火山方舟 Agent Plan", url: "https://ark.cn-beijing.volces.com/api/plan/v3" },
  { id: "volcengine-coding", label: "火山方舟 Coding Plan", url: "https://ark.cn-beijing.volces.com/api/coding/v3" },
  { id: "deepseek", label: "DeepSeek", url: "https://api.deepseek.com/v1" },
  { id: "openrouter", label: "OpenRouter", url: "https://openrouter.ai/api/v1" },
  { id: "ollama", label: "Ollama（本地）", url: "http://localhost:11434/v1" },
  { id: "qwen", label: "通义千问 / 百炼", url: "https://dashscope.aliyuncs.com/compatible-mode/v1" },
  { id: "siliconflow", label: "硅基流动", url: "https://api.siliconflow.cn/v1" },
];
const EMPTY: ModelSettingsData = { provider: "custom", base_url: "", api_key_set: false, models: [], enabled: false };
const inputStyle = "w-full rounded-lg border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-accent";

export function ModelSettings({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const [profilesOpen, setProfilesOpen] = useState(false);
  const [config, setConfig] = useState<ModelSettingsData>(EMPTY);
  const [key, setKey] = useState("");
  const [savedBase, setSavedBase] = useState("");
  const [modelText, setModelText] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [loadRevision, setLoadRevision] = useState(0);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<"save" | "discover" | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [profileText, setProfileText] = useState("");
  useEffect(() => {
    let alive = true;
    setLoading(true); setError(""); setLoaded(false);
    modelSettings.get().then(c => {
      if (alive) { setLoaded(true); setConfig({ ...c, provider: (!c.provider || c.provider === "openai-compatible") ? "custom" : c.provider }); setSavedBase(c.base_url); setModelText((c.models || []).join("\n")); }
    }).catch(e => { if (alive) setError(e.message); }).finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [loadRevision]);
  const patch = (p: Partial<ModelSettingsData>) => { setConfig(c => ({ ...c, ...p })); setNotice(""); };
  const payload = (): ModelSettingsInput => ({
    provider: config.provider, base_url: config.base_url.trim(), enabled: config.enabled,
    models: [...new Set(modelText.split(/[\n,]/).map(m => m.trim()).filter(Boolean))],
    ...(config.policies ? { policies: config.policies } : {}),
    ...(key ? { api_key: key } : {}),
  });
  const discover = async () => {
    setBusy("discover"); setError(""); setNotice("");
    try {
      const r = await modelSettings.discover({ provider: config.provider, base_url: config.base_url.trim(), ...(key ? { api_key: key } : {}) });
      setModelText([...new Set([...modelText.split(/[\n,]/).map(m => m.trim()).filter(Boolean), ...r.models])].join("\n"));
      setNotice(r.notice || (r.source === "preset"
        ? "已提供厂商预设候选，尚未验证密钥和模型调用权限。可手动调整后保存。"
        : r.models.length ? `获取到 ${r.models.length} 个模型，可在下方选择或手动填写。` : "该服务未返回模型，可以直接手动填写模型名称。"));
    } catch (e) { setError(`获取模型失败：${(e as Error).message}。可以手动填写模型名称后保存。`); }
    finally { setBusy(null); }
  };
  const save = async () => {
    if (!loaded) return;
    setBusy("save"); setError(""); setNotice("");
    try {
      const saved = await modelSettings.save(payload());
      setConfig(saved); setSavedBase(saved.base_url); setKey(""); setModelText((saved.models || []).join("\n"));
      setNotice("配置已保存，对下一次发送生效。"); onSaved();
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(null); }
  };
  if (profilesOpen) return <Suspense fallback={<p role="status">正在加载连接配置…</p>}><ModelProfilesDialog onClose={() => { setProfilesOpen(false); setLoadRevision(n => n + 1); }} onSaved={onSaved} /></Suspense>;
  return <Dialog.Root open onOpenChange={open => { if (!open && !busy) onClose(); }}>
    <Dialog.Portal>
      <Dialog.Overlay className="fixed inset-0 z-50 bg-black/30" />
      <Dialog.Content className="fixed left-1/2 top-1/2 z-50 max-h-[90vh] w-[min(640px,calc(100vw-24px))] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-2xl border border-border bg-surface p-6 shadow-xl">
        <div className="flex items-center justify-between gap-4">
          <Dialog.Title className="font-serif text-xl text-ink">模型配置</Dialog.Title>
          <Dialog.Close asChild><Button variant="ghost" size="sm" disabled={!!busy} aria-label="关闭模型配置">关闭</Button></Dialog.Close>
        </div>
        <Dialog.Description className="mt-2 text-sm text-muted">配置你的模型服务。支持 OpenAI 兼容接口，也可手动填写模型名称。</Dialog.Description>
        <ActionChip className="mt-3" disabled={!!busy} onClick={() => setProfilesOpen(true)} icon="gear">命名连接与能力检测</ActionChip>
        {loading ? <p className="py-8 text-muted">正在读取配置…</p> : !loaded ? <Button variant="outline" onClick={() => setLoadRevision(n => n + 1)}>重新读取配置</Button> : <form className="mt-5 flex flex-col gap-4" onSubmit={e => { e.preventDefault(); void save(); }}>
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

// ---- ModelSettingsPanel: the drawer-tab editor (server-wide /settings: endpoint, key, model list, reasoning) ----

// ModelSettings — the endpoint, the key and the model list, without editing a
// file and restarting.
//
// Saved to the user's own ~/.orka/orka.json (the panel shows the path), applied
// to the running process immediately, and layered OVER config.yaml and the
// environment so what is saved here is the last word.
//
// Two things this panel is careful about:
//
//   - The API key is write-only. It is never sent to the browser; the field
//     shows whether one is set and stays empty otherwise. Leaving it empty on
//     save KEEPS the stored key, so changing a URL never means re-typing it.
//   - Test before save. A typo in the endpoint would otherwise leave the whole
//     deployment unable to reach any model, with the panel as the only way back.

// A row of the model list. Held as objects so React keys stay stable while the
// text is edited — keying by value makes an input lose focus on every keystroke.
type Row = { id: number; name: string };

let nextID = 1;
const toRows = (names: string[]): Row[] => names.map((n) => ({ id: nextID++, name: n }));

export function ModelSettingsPanel() {
  const [view, setView] = useState<SettingsView | null>(null);
  const [baseURL, setBaseURL] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [rows, setRows] = useState<Row[]>([]);
  const [maxTokens, setMaxTokens] = useState("");
  const [reasoning, setReasoning] = useState("");
  const [busy, setBusy] = useState<"" | "save" | "test">("");
  const [probe, setProbe] = useState<{ ok: boolean; text: string } | null>(null);

  const load = (v: SettingsView) => {
    setView(v);
    // Seed from EFFECTIVE, not from what was saved: most fields fall back to
    // config.yaml or the environment, and empty boxes on a working deployment
    // would suggest nothing is configured.
    setBaseURL(v.effective.base_url || "");
    setRows(toRows(v.effective.models || []));
    setMaxTokens(v.effective.max_tokens ? String(v.effective.max_tokens) : "");
    setReasoning(v.saved.reasoning ? JSON.stringify(v.saved.reasoning) : "");
    setApiKey("");
  };

  useEffect(() => {
    settingsApi.get().then(load).catch(() => setView(null));
  }, []);

  // patch builds the payload. Only the key is conditional — everything else is
  // sent as shown, because the panel shows the effective value and the user
  // edited exactly what they see.
  const patch = () => {
    const models = rows.map((r) => r.name.trim()).filter(Boolean);
    let parsed: Record<string, unknown> | undefined;
    const raw = reasoning.trim();
    if (raw) {
      try {
        parsed = JSON.parse(raw);
      } catch {
        throw new Error("推理参数不是合法 JSON");
      }
    }
    return {
      base_url: baseURL.trim(),
      models,
      // -1 is an explicit "no cap": 0 cannot be told from unset over JSON.
      max_tokens: maxTokens.trim() === "" ? -1 : Number(maxTokens),
      reasoning: parsed ?? {},
      ...(apiKey.trim() ? { api_key: apiKey.trim() } : {}),
    };
  };

  const test = async () => {
    setBusy("test");
    setProbe(null);
    try {
      const r = await settingsApi.test(patch());
      setProbe({
        ok: r.ok,
        text: r.ok ? `${r.model} 可用${r.tokens ? ` · ${r.tokens} tokens` : ""}` : r.error || "失败",
      });
    } catch (e) {
      setProbe({ ok: false, text: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy("");
    }
  };

  const save = async () => {
    setBusy("save");
    try {
      load(await settingsApi.save(patch()));
      toast("已保存并生效", "success");
      // The model dropdown is server-driven; it reads the new list on next open.
    } catch (e) {
      toastError(e instanceof Error ? e.message : "保存失败");
    } finally {
      setBusy("");
    }
  };

  if (!view) return <div className="px-4 py-6 text-[13px] text-faint">加载设置…</div>;

  const field = "w-full rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] outline-none focus:border-accent/50";
  const label = "mb-1 block text-[12px] text-muted";

  return (
    <div className="space-y-4 p-3">
      <div className="text-[12px] leading-relaxed text-faint">
        保存到 <code className="rounded bg-surface2 px-1">{view.path}</code>,立即生效,无需重启。
        这里的值会覆盖 config.yaml 和环境变量。
      </div>

      <div>
        <label className={label}>接口地址(OpenAI 兼容)</label>
        <input value={baseURL} onChange={(e) => setBaseURL(e.target.value)} placeholder="https://…/v1" className={field} />
      </div>

      <div>
        <label className={label}>
          API 密钥
          <span className="ml-1.5 text-faint">
            {view.effective.has_key ? "· 已设置(留空表示不改)" : "· 未设置"}
          </span>
        </label>
        <input
          type="password"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          placeholder={view.effective.has_key ? "••••••••  留空保持不变" : "填入密钥"}
          autoComplete="off"
          className={field}
        />
      </div>

      <div>
        <label className={label}>模型列表 · 第一个是默认</label>
        <div className="space-y-1.5">
          {rows.map((r, i) => (
            <div key={r.id} className="flex items-center gap-1.5">
              <span className="w-10 shrink-0 text-[11px] text-faint">{i === 0 ? "默认" : `#${i + 1}`}</span>
              <input
                value={r.name}
                onChange={(e) => setRows((xs) => xs.map((x) => (x.id === r.id ? { ...x, name: e.target.value } : x)))}
                placeholder="模型名"
                className={field}
              />
              <button
                onClick={() => setRows((xs) => { const n = [...xs]; const j = i - 1; if (j < 0) return n; [n[i], n[j]] = [n[j], n[i]]; return n; })}
                disabled={i === 0}
                title="上移(移到第一个即为默认)"
                className="shrink-0 rounded-md px-1.5 py-1 text-[12px] text-faint hover:bg-surface2 disabled:opacity-30"
              >
                ↑
              </button>
              <button
                onClick={() => setRows((xs) => xs.filter((x) => x.id !== r.id))}
                aria-label="删除这一行"
                className="shrink-0 rounded-md px-1.5 py-1 text-faint hover:bg-surface2 hover:text-accent"
              >
                <Icon name="trash" size={12} />
              </button>
            </div>
          ))}
        </div>
        <button
          onClick={() => setRows((xs) => [...xs, { id: nextID++, name: "" }])}
          className="mt-1.5 rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40"
        >
          + 添加模型
        </button>
      </div>

      <div>
        <label className={label}>单回合输出上限(留空 = 用服务商默认)</label>
        <input value={maxTokens} onChange={(e) => setMaxTokens(e.target.value)} placeholder="例如 32768" className={field} />
      </div>

      <div>
        <label className={label}>
          推理控制 · JSON
          <span className="ml-1.5 text-faint">各家字段不同</span>
        </label>
        <input
          value={reasoning}
          onChange={(e) => setReasoning(e.target.value)}
          placeholder={'{"thinking":{"type":"disabled"}}'}
          className={field + " font-mono text-[12px]"}
        />
        {/* Not a dropdown of known providers on purpose: there is no agreed
            field, and this deployment's own endpoint honours `thinking` for one
            model and ignores `reasoning_effort` entirely. A list of guesses
            would be wrong more often than a box you can paste the right thing
            into. */}
        <div className="mt-1 text-[11px] leading-relaxed text-faint">
          常见写法:<code>{'{"reasoning_effort":"low"}'}</code>(OpenAI 系)、
          <code>{'{"thinking":{"type":"disabled"}}'}</code>(方舟 / 智谱)、
          <code>{'{"enable_thinking":false}'}</code>(千问 / vLLM)。
          先用「测试连接」确认服务商是否真的认这个字段。
        </div>
      </div>

      <div className="flex items-center gap-2 border-t border-border pt-3">
        <button
          onClick={save}
          disabled={busy !== ""}
          className="rounded-lg bg-accent px-3 py-1.5 text-[12.5px] text-white hover:brightness-105 disabled:opacity-40"
        >
          {busy === "save" ? "保存中…" : "保存并生效"}
        </button>
        <button
          onClick={test}
          disabled={busy !== ""}
          className="rounded-lg border border-border px-3 py-1.5 text-[12.5px] text-muted hover:border-accent/40 disabled:opacity-40"
        >
          {busy === "test" ? "测试中…" : "测试连接"}
        </button>
        {probe && (
          <span className={"text-[12px] " + (probe.ok ? "text-ok" : "text-accent")}>
            {probe.ok ? "✓ " : "✕ "}
            {probe.text}
          </span>
        )}
      </div>
    </div>
  );
}
