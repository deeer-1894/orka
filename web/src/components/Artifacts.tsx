import { useEffect, useMemo, useState } from "react";
import { artifacts as artApi, subscribeArtifact } from "../api";
import type { Artifact, ArtifactBlock, ArtifactVersion } from "../types";
import { ArtifactRenderer } from "./ArtifactRenderer";
import { Icon } from "./Icon";
import { toast, toastError } from "../lib/toast";
import { useOverlay } from "../lib/useOverlay";
import { confirmDialog } from "../lib/confirm";

const KIND_ICON: Record<string, string> = {
  pr_review: "🔀", architecture: "🗺️", incident: "🚨", checklist: "✅", audit: "🔍", custom: "📊",
};
const kindIcon = (k: string) => KIND_ICON[k] || "📄";
// Human kind labels + an accent colour, so a card communicates what it is.
const KIND_META: Record<string, { label: string; color: string }> = {
  pr_review: { label: "代码评审", color: "#7c9fe8" },
  architecture: { label: "架构", color: "#b88ad9" },
  incident: { label: "事故复盘", color: "#e8927c" },
  checklist: { label: "清单", color: "#7fb88a" },
  audit: { label: "审计", color: "#e8c97c" },
  custom: { label: "报告", color: "#c45c3e" },
};
const kindMeta = (k: string) => KIND_META[k] || { label: k || "页面", color: "#9b978d" };
const fmtTime = (ms: number) => {
  const d = new Date(ms);
  return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
};
const relTime = (ms: number) => {
  const diff = Date.now() - ms;
  if (diff < 60000) return "刚刚";
  if (diff < 3600000) return Math.floor(diff / 60000) + " 分钟前";
  if (diff < 86400000) return Math.floor(diff / 3600000) + " 小时前";
  if (diff < 7 * 86400000) return Math.floor(diff / 86400000) + " 天前";
  return fmtTime(ms);
};

// ── Gallery: a grid of the user's artifacts (lives in the drawer) ──────────────
type Sort = "updated" | "name";
export function ArtifactGallery({ onOpen }: { onOpen: (id: string) => void }) {
  const [list, setList] = useState<Artifact[] | null>(null);
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<Sort>("updated");
  const refresh = () => artApi.list().then((r) => setList(r.artifacts || [])).catch(() => setList([]));
  useEffect(() => { refresh(); }, []);

  const copyLink = (a: Artifact) => {
    if (a.visibility === "public" && a.share_token) {
      navigator.clipboard?.writeText(artApi.publicURL(a.slug, a.share_token));
      toast("公开链接已复制");
    } else {
      toast("先在页面里开启“公开链接”再复制", "info");
    }
  };
  const del = async (a: Artifact) => {
    if (!(await confirmDialog({ title: `删除「${a.title}」?`, body: "删除后该页面与其公开链接都会失效。", confirmText: "删除", danger: true }))) return;
    try { await artApi.del(a.artifact_id); toast("已删除"); refresh(); } catch { toastError("删除失败"); }
  };

  const q = query.trim().toLowerCase();
  const shown = useMemo(() => {
    const arr = (list || []).filter((a) => !q || (a.title || "").toLowerCase().includes(q));
    arr.sort((x, y) => (sort === "name" ? (x.title || "").localeCompare(y.title || "") : y.updated_at - x.updated_at));
    return arr;
  }, [list, q, sort]);

  if (list === null) return <div className="p-4 text-[13px] text-faint">加载…</div>;
  if (list.length === 0)
    return (
      <div className="p-4">
        <div className="rounded-xl border border-dashed border-border p-6 text-center text-[13px] text-muted">
          还没有 Artifact。<br />
          <span className="text-faint">让 Orka 把工作整理成一个可分享的实时页面 —— 它会随会话进展自动更新。</span>
        </div>
      </div>
    );
  return (
    <div className="p-3">
      {list.length > 4 && (
        <div className="mb-2.5 flex items-center gap-1.5">
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索页面…"
            className="min-w-0 flex-1 rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[12.5px] outline-none focus:border-accent/50"
          />
          <select value={sort} onChange={(e) => setSort(e.target.value as Sort)} className="shrink-0 rounded-lg border border-border bg-surface px-1.5 py-1.5 text-[12px] text-muted outline-none" title="排序">
            <option value="updated">最近更新</option>
            <option value="name">名称</option>
          </select>
        </div>
      )}
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        {shown.map((a) => {
          const km = kindMeta(a.kind);
          return (
            <div
              key={a.artifact_id}
              onClick={() => onOpen(a.artifact_id)}
              className="group relative cursor-pointer overflow-hidden rounded-xl border border-border bg-surface2/40 p-3 transition hover:border-accent/40"
            >
              <span aria-hidden className="absolute left-0 top-0 h-full w-[3px]" style={{ background: km.color }} />
              <div className="flex w-full items-center gap-2">
                <span className="text-[16px]">{kindIcon(a.kind)}</span>
                <span className="min-w-0 flex-1 truncate text-[13.5px] font-medium text-ink">{a.title}</span>
                {a.visibility === "public" ? <Icon name="globe" size={13} className="shrink-0 text-accent" /> : a.visibility === "shared" ? <Icon name="link" size={13} className="shrink-0 text-muted" /> : null}
              </div>
              <div className="mt-1.5 flex w-full items-center gap-1.5 text-[11px] text-faint">
                <span className="rounded-full px-1.5 py-px" style={{ color: km.color, background: km.color + "1a" }}>{km.label}</span>
                <span>v{a.current_version}</span>
                <span>·</span>
                <span title={fmtTime(a.updated_at)}>{relTime(a.updated_at)}</span>
              </div>
              {/* hover actions — act on a page without opening it first */}
              <div className="absolute right-2 top-2 flex gap-0.5 opacity-0 transition group-hover:opacity-100">
                <button onClick={(e) => { e.stopPropagation(); copyLink(a); }} title="复制公开链接" aria-label="复制公开链接" className="grid h-6 w-6 place-items-center rounded-md bg-surface/80 text-faint hover:text-accent">
                  <Icon name="link" size={13} />
                </button>
                <button onClick={(e) => { e.stopPropagation(); del(a); }} title="删除" aria-label="删除页面" className="grid h-6 w-6 place-items-center rounded-md bg-surface/80 text-faint hover:text-accent">
                  <Icon name="trash" size={13} />
                </button>
              </div>
            </div>
          );
        })}
        {shown.length === 0 && <div className="col-span-full py-6 text-center text-[12px] text-faint">无匹配页面</div>}
      </div>
    </div>
  );
}

// ── In-chat banner: a live card for the conversation's artifact ──────────────
export function ArtifactBanner({ conversationId, onOpen }: { conversationId: string; onOpen: (id: string) => void }) {
  const [art, setArt] = useState<Artifact | null>(null);
  const [bump, setBump] = useState(false);

  useEffect(() => {
    if (!conversationId) { setArt(null); return; }
    let alive = true;
    artApi.byConversation(conversationId).then((a) => alive && setArt(a)).catch(() => alive && setArt(null));
    return () => { alive = false; };
  }, [conversationId]);

  // Live: re-fetch meta on each version bump so the card reflects v + a pulse.
  useEffect(() => {
    if (!art) return;
    const unsub = subscribeArtifact(art.artifact_id, () => {
      setBump(true);
      setTimeout(() => setBump(false), 1500);
      artApi.byConversation(conversationId).then(setArt).catch(() => {});
    });
    return unsub;
  }, [art?.artifact_id, conversationId]);

  if (!art) return null;
  return (
    <button
      onClick={() => onOpen(art.artifact_id)}
      className="mx-auto mb-3 flex w-full max-w-3xl items-center gap-2.5 rounded-xl border border-accent/30 bg-accentsoft/40 px-3.5 py-2.5 text-left transition hover:border-accent/60"
    >
      <span className="text-[18px]">{kindIcon(art.kind)}</span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13.5px] font-medium text-ink">{art.title}</span>
        <span className="text-[11px] text-faint">实时页面 · v{art.current_version} · {art.visibility === "public" ? "公开" : art.visibility === "shared" ? "已分享" : "私有"}</span>
      </span>
      {bump && <span className="rounded-full bg-ok/15 px-2 py-0.5 text-[11px] text-ok">● 已更新</span>}
      <span className="text-[12px] text-accent">打开 →</span>
    </button>
  );
}

// ── Viewer: in-app modal with live updates, versions, sharing ────────────────
// ArtifactBody is the artifact view (header + version select + share + render),
// shared by the inline drawer pane and the (legacy) modal.
function ArtifactBody({ artifactId, onClose, inline, backLabel }: { artifactId: string; onClose: () => void; inline?: boolean; backLabel?: string }) {
  const [art, setArt] = useState<Artifact | null>(null);
  const [ver, setVer] = useState<ArtifactVersion | null>(null);
  const [viewing, setViewing] = useState(0); // 0 = latest
  const [live, setLive] = useState(false);
  const [showShare, setShowShare] = useState(false);

  const load = (version = 0) =>
    artApi.get(artifactId, version).then((r) => { setArt(r.artifact); setVer(r.version); }).catch(() => {});

  useEffect(() => { load(viewing); /* eslint-disable-next-line */ }, [artifactId, viewing]);

  // Live: when a new version is published, flash + reload (only while viewing latest).
  useEffect(() => {
    const unsub = subscribeArtifact(artifactId, (v) => {
      if (viewing === 0) {
        setLive(true);
        setTimeout(() => setLive(false), 1500);
        load(0);
      } else {
        setArt((a) => (a ? { ...a, current_version: v } : a)); // badge the "newer exists"
      }
    });
    return unsub;
  }, [artifactId, viewing]);

  return (
    <div
      className={inline ? "flex h-full flex-col bg-surface" : "pop-in flex max-h-[86vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-border bg-surface shadow-xl"}
      onClick={inline ? undefined : (e) => e.stopPropagation()}
    >
      <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
        {inline ? (
          <button onClick={onClose} className="shrink-0 text-[12px] text-muted hover:text-accent" title="返回画廊">← {backLabel || "画廊"}</button>
        ) : (
          <span className="text-[16px]">{kindIcon(art?.kind || "")}</span>
        )}
        <span className="min-w-0 flex-1 truncate text-[14px] font-medium text-ink">{art?.title || "Artifact"}</span>
        {live && <span className="rounded-full bg-ok/15 px-2 py-0.5 text-[11px] text-ok">● 已更新</span>}
        {art && art.current_version > 1 && (
          <select
            value={viewing}
            onChange={(e) => setViewing(Number(e.target.value))}
            className="rounded-lg border border-border bg-surface2 px-1.5 py-1 text-[12px] text-muted outline-none"
            title="版本"
          >
            <option value={0}>最新 v{art.current_version}</option>
            {Array.from({ length: art.current_version }, (_, i) => art.current_version - i).map((v) => (
              <option key={v} value={v}>v{v}</option>
            ))}
          </select>
        )}
        {ver && (
          <button
            onClick={() => { navigator.clipboard?.writeText(blocksToMarkdown(art?.title || "", ver.blocks)); toast("已复制为 Markdown"); }}
            className="text-[12px] text-muted hover:text-accent hover:underline"
            title="把这个页面复制为 Markdown 文本"
          >
            复制 MD
          </button>
        )}
        <button onClick={() => setShowShare((x) => !x)} className={"text-[12px] hover:underline " + (showShare ? "text-accent font-medium" : "text-muted")}>分享</button>
        {!inline && <button onClick={onClose} className="ml-1 text-faint hover:text-ink"><Icon name="close" size={14} /></button>}
      </div>

      {showShare && art && <ShareBar art={art} onChange={(a) => setArt(a)} />}

      <div className="flex-1 overflow-y-auto px-5 py-4">
        {ver ? <ArtifactRenderer blocks={ver.blocks} /> : <div className="text-[13px] text-faint">加载…</div>}
        {ver?.note && <div className="mt-4 border-t border-border pt-2 text-[11px] text-faint">本版更新: {ver.note}</div>}
      </div>
    </div>
  );
}

// ArtifactViewer is the legacy modal (kept for the public page / fallbacks).
export function ArtifactViewer({ artifactId, onClose }: { artifactId: string; onClose: () => void }) {
  useOverlay(onClose);
  return (
    <div className="overlay-in fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
      <ArtifactBody artifactId={artifactId} onClose={onClose} />
    </div>
  );
}

// ArtifactPane renders the artifact large + persistent inside the drawer 舞台 —
// "conversation + preview" instead of a transient modal.
export function ArtifactPane({ artifactId, onBack }: { artifactId: string; onBack: () => void }) {
  return <ArtifactBody artifactId={artifactId} onClose={onBack} inline backLabel="画廊" />;
}

// ShareBar: owner controls — public link toggle + copy. (Per-user email sharing
// reuses the same endpoint but is kept minimal here; public link is the headline.)
function ShareBar({ art, onChange }: { art: Artifact; onChange: (a: Artifact) => void }) {
  const [busy, setBusy] = useState(false);
  const isPublic = art.visibility === "public";
  const togglePublic = async () => {
    setBusy(true);
    try {
      const r = await artApi.setPublic(art.artifact_id, !isPublic);
      onChange({ ...art, visibility: r.visibility as Artifact["visibility"], share_token: r.share_token });
      toast(isPublic ? "已停用公开链接" : "已生成公开链接");
    } catch { toastError("操作失败"); } finally { setBusy(false); }
  };
  const copy = () => {
    if (!art.share_token) return;
    navigator.clipboard?.writeText(artApi.publicURL(art.slug, art.share_token));
    toast("链接已复制");
  };
  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-border bg-surface2/30 px-4 py-2 text-[12.5px]">
      <button onClick={togglePublic} disabled={busy} className={"rounded-lg px-2.5 py-1 " + (isPublic ? "bg-accentsoft text-accent" : "border border-border text-muted hover:border-accent/40")}>
        {isPublic ? "🌐 公开链接已开启" : "🌐 生成公开链接"}
      </button>
      {isPublic && art.share_token && (
        <>
          <input readOnly value={artApi.publicURL(art.slug, art.share_token)} className="min-w-0 flex-1 rounded-lg border border-border bg-surface px-2 py-1 text-[11.5px] text-muted outline-none" />
          <button onClick={copy} className="rounded-lg border border-border px-2 py-1 text-muted hover:border-accent/40">复制</button>
        </>
      )}
      <span className="text-faint">任何拿到链接的人都能看到这个页面(只读),并随你更新实时刷新。</span>
    </div>
  );
}

// blocksToMarkdown serializes an artifact's typed blocks to portable Markdown, so
// a report can leave the app as text (paste into a doc / PR / ticket), not only
// as a live link. Visual-only blocks (html, charts) degrade to a textual summary.
function blocksToMarkdown(title: string, blocks: ArtifactBlock[]): string {
  const sv = (v: unknown) => (v == null ? "" : String(v));
  const out: string[] = [];
  if (title) out.push(`# ${title}\n`);
  for (const b of blocks) {
    const d = (b.data || {}) as Record<string, unknown>;
    switch (b.type) {
      case "heading": {
        const lvl = Math.min(3, Math.max(1, Number(d.level) || 1));
        out.push(`${"#".repeat(lvl)} ${sv(d.text)}\n`);
        break;
      }
      case "markdown":
        out.push(sv(d.text) + "\n");
        break;
      case "metric":
        out.push(`**${sv(d.label)}**: ${sv(d.value)}${d.delta ? ` (${sv(d.delta)})` : ""}\n`);
        break;
      case "badge": {
        const items = (Array.isArray(d.items) ? d.items : [d]) as Record<string, unknown>[];
        out.push(items.map((it) => `\`${sv(it.label)}\``).join(" ") + "\n");
        break;
      }
      case "checklist": {
        const items = (d.items as Record<string, unknown>[]) || [];
        out.push(items.map((it) => `- [${sv(it.status) === "done" ? "x" : " "}] ${sv(it.label)}`).join("\n") + "\n");
        break;
      }
      case "table": {
        const cols = (d.columns as string[]) || [];
        const rows = (d.rows as unknown[][]) || [];
        if (cols.length) {
          out.push(`| ${cols.join(" | ")} |`);
          out.push(`| ${cols.map(() => "---").join(" | ")} |`);
          for (const r of rows) out.push(`| ${r.map(sv).join(" | ")} |`);
          out.push("");
        }
        break;
      }
      case "timeline": {
        const evs = (d.events as Record<string, unknown>[]) || [];
        out.push(evs.map((e) => `- ${e.time ? `**${sv(e.time)}** — ` : ""}${sv(e.title)}${e.detail ? `: ${sv(e.detail)}` : ""}`).join("\n") + "\n");
        break;
      }
      case "diff":
        out.push("```diff\n" + sv(d.patch) + "\n```\n");
        break;
      case "code":
        out.push("```" + sv(d.language) + "\n" + sv(d.text) + "\n```\n");
        break;
      case "mermaid":
        out.push("```mermaid\n" + sv(d.src) + "\n```\n");
        break;
      case "chart": {
        const pts = (Array.isArray(d.data) ? d.data : []) as Record<string, unknown>[];
        out.push(`**${sv(d.title) || "图表"}**`);
        out.push(pts.map((p) => `- ${sv(p.label)}: ${sv(p.value)}`).join("\n") + "\n");
        break;
      }
      default:
        break;
    }
  }
  return out.join("\n").replace(/\n{3,}/g, "\n\n").trim() + "\n";
}

// ── Public standalone page (/a/:slug?t=token) — read-only, live ──────────────
export function PublicArtifactPage({ slug, token }: { slug: string; token: string }) {
  const [data, setData] = useState<{ artifact: Artifact; version: ArtifactVersion } | null>(null);
  const [err, setErr] = useState(false);
  const [live, setLive] = useState(false);

  const base = "/api/v1/controller/pub/a/" + encodeURIComponent(slug);
  const load = () =>
    fetch(`${base}?token=${encodeURIComponent(token)}`)
      .then((r) => (r.ok ? r.json() : Promise.reject()))
      .then((j) => setData(j.data))
      .catch(() => setErr(true));

  useEffect(() => { load(); /* eslint-disable-next-line */ }, [slug, token]);

  // Public live updates via native EventSource on the public stream.
  useEffect(() => {
    const es = new EventSource(`${base}/stream?token=${encodeURIComponent(token)}`);
    es.onmessage = () => { setLive(true); setTimeout(() => setLive(false), 1500); load(); };
    es.onerror = () => es.close();
    return () => es.close();
    /* eslint-disable-next-line */
  }, [slug, token]);

  if (err) return <div className="grid min-h-screen place-items-center text-[14px] text-muted">页面不存在或链接已失效。</div>;
  if (!data) return <div className="grid min-h-screen place-items-center text-[14px] text-faint">加载…</div>;
  return (
    <div className="min-h-screen bg-bg">
      <header className="sticky top-0 z-10 flex items-center gap-2 border-b border-border bg-surface/90 px-5 py-3 backdrop-blur">
        <span className="grid h-6 w-6 place-items-center rounded-md bg-accent text-[12px] text-white">O</span>
        <span className="text-[15px] font-medium text-ink">{data.artifact.title}</span>
        <span className="text-[11px] text-faint">v{data.version.version}</span>
        {live && <span className="rounded-full bg-ok/15 px-2 py-0.5 text-[11px] text-ok">● 实时更新</span>}
        <span className="ml-auto text-[11px] text-faint">由 Orka 生成 · 实时</span>
      </header>
      <main className="mx-auto max-w-3xl px-5 py-8">
        <ArtifactRenderer blocks={data.version.blocks} />
      </main>
    </div>
  );
}
