import { useEffect, useState } from "react";
import { settings as settingsApi, type SettingsView } from "../api";
import { Icon } from "./Icon";
import { toast, toastError } from "../lib/toast";

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
