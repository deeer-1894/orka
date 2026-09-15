import { useEffect, useState } from "react";
import { api } from "../api";
import type { Connector } from "../types";
import { toast } from "../lib/toast";
import { Icon } from "./Icon";
import { PanelEmpty as Blank } from "./PanelEmpty";
function parseHeaders(text: string): Record<string, string> {
  const h: Record<string, string> = {};
  for (const ln of text.split("\n")) {
    const i = ln.indexOf(":");
    if (i > 0) h[ln.slice(0, i).trim()] = ln.slice(i + 1).trim();
  }
  return h;
}

// ConnectorsPanel manages external MCP servers — Orka's gateway can drive any of
// them, so this turns the agent into an integration platform.
export function ConnectorsPanel() {
  const [conns, setConns] = useState<Connector[]>([]);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [transport, setTransport] = useState("streamable_http");
  const [url, setUrl] = useState("");
  const [headers, setHeaders] = useState("");
  const [probe, setProbe] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = () => api.listConnectors().then((r) => setConns(r.connectors || [])).catch(() => {});
  useEffect(() => { refresh(); }, []);

  const draft = () => ({ name, transport, url, headers: parseHeaders(headers) });
  const test = async () => {
    setBusy(true);
    setProbe(null);
    try {
      const r = await api.testConnector(draft());
      setProbe(r.ok ? `✓ 连接成功，发现 ${r.tools?.length || 0} 个工具` : `✕ ${r.error}`);
    } catch {
      setProbe("✕ 测试失败");
    } finally {
      setBusy(false);
    }
  };
  const save = async () => {
    if (!name.trim() || !url.trim()) return;
    setBusy(true);
    try {
      await api.createConnector(draft());
      setName(""); setUrl(""); setHeaders(""); setProbe(null); setAdding(false);
      toast("已添加集成，工具将在下次运行可用", "success");
      refresh();
    } finally { setBusy(false); }
  };

  return (
    <div className="p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[12px] text-faint">外部 MCP 集成 · {conns.length}</span>
        <button onClick={() => setAdding((o) => !o)} className="rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40">
          {adding ? "取消" : "+ 添加集成"}
        </button>
      </div>

      {adding && (
        <div className="mb-3 space-y-2 rounded-xl border border-border bg-surface2/40 p-3">
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="名称，如 GitHub" className="w-full rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] outline-none focus:border-accent/50" />
          <div className="flex gap-2">
            <select value={transport} onChange={(e) => setTransport(e.target.value)} className="rounded-lg border border-border bg-surface px-2 py-1.5 text-[13px] outline-none">
              <option value="streamable_http">streamable_http</option>
              <option value="http">http (SSE)</option>
            </select>
            <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://mcp.example.com/sse" className="flex-1 rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] outline-none focus:border-accent/50" />
          </div>
          <textarea value={headers} onChange={(e) => setHeaders(e.target.value)} rows={2} placeholder="鉴权头，每行 Key: Value，如&#10;Authorization: Bearer sk-..." className="w-full resize-none rounded-lg border border-border bg-surface px-2.5 py-2 font-mono text-[12px] outline-none focus:border-accent/50" />
          {probe && <div className={"text-[12px] " + (probe.startsWith("✓") ? "text-ok" : "text-accent")}>{probe}</div>}
          <div className="flex items-center gap-2">
            <button onClick={test} disabled={busy || !url.trim()} className="rounded-lg border border-border px-2.5 py-1.5 text-[13px] text-muted hover:border-accent/40 disabled:opacity-40">测试连接</button>
            <button onClick={save} disabled={busy || !name.trim() || !url.trim()} className="ml-auto rounded-lg bg-accent px-3 py-1.5 text-[13px] text-white disabled:opacity-40">添加</button>
          </div>
        </div>
      )}

      {conns.length === 0 && !adding && (
        <Blank icon="plug" title="还没有外部工具" action={{ label: "+ 添加连接器", onClick: () => setAdding(true) }}>
          接入一个 MCP 服务后,它提供的工具会自动出现在 Orka 的工具表里,和内置工具一样被调用。
        </Blank>
      )}
      <div className="space-y-1.5">
        {conns.map((cn) => (
          <div key={cn.connector_id} className="flex items-center gap-2 rounded-xl border border-border bg-surface2/40 px-3 py-2.5">
            <Icon name="plug" size={15} className="text-muted" />
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] text-ink">{cn.name}</div>
              <div className="truncate text-[11px] text-faint">{cn.transport} · {cn.url}</div>
            </div>
            <button onClick={() => api.deleteConnector(cn.connector_id).then(refresh)} aria-label="移除集成" className="text-faint hover:text-accent"><Icon name="close" size={13} /></button>
          </div>
        ))}
      </div>
    </div>
  );
}
