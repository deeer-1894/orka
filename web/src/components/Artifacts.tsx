import { useEffect, useState } from "react";
import { artifacts as artApi, subscribeArtifact } from "../api";
import type { Artifact, ArtifactVersion } from "../types";
import { ArtifactRenderer } from "./ArtifactRenderer";
import { toast, toastError } from "../lib/toast";

const KIND_ICON: Record<string, string> = {
  pr_review: "🔀", architecture: "🗺️", incident: "🚨", checklist: "✅", audit: "🔍", custom: "📊",
};
const kindIcon = (k: string) => KIND_ICON[k] || "📄";
const fmtTime = (ms: number) => {
  const d = new Date(ms);
  return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
};

// ── Gallery: a grid of the user's artifacts (lives in the drawer) ──────────────
export function ArtifactGallery({ onOpen }: { onOpen: (id: string) => void }) {
  const [list, setList] = useState<Artifact[] | null>(null);
  const refresh = () => artApi.list().then((r) => setList(r.artifacts || [])).catch(() => setList([]));
  useEffect(() => { refresh(); }, []);

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
    <div className="grid grid-cols-1 gap-2 p-3 sm:grid-cols-2">
      {list.map((a) => (
        <button
          key={a.artifact_id}
          onClick={() => onOpen(a.artifact_id)}
          className="group flex flex-col items-start rounded-xl border border-border bg-surface2/40 p-3 text-left transition hover:border-accent/40"
        >
          <div className="flex w-full items-center gap-2">
            <span className="text-[16px]">{kindIcon(a.kind)}</span>
            <span className="min-w-0 flex-1 truncate text-[13.5px] font-medium text-ink">{a.title}</span>
            {a.visibility === "public" ? <span title="公开链接">🌐</span> : a.visibility === "shared" ? <span title="已分享">🔗</span> : null}
          </div>
          <div className="mt-1.5 flex w-full items-center gap-2 text-[11px] text-faint">
            <span>v{a.current_version}</span>
            <span>·</span>
            <span>{fmtTime(a.updated_at)}</span>
          </div>
        </button>
      ))}
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
export function ArtifactViewer({ artifactId, onClose }: { artifactId: string; onClose: () => void }) {
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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
      <div className="flex max-h-[86vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-border bg-surface shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
          <span className="text-[16px]">{kindIcon(art?.kind || "")}</span>
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
          <button onClick={() => setShowShare((x) => !x)} className={"text-[12px] hover:underline " + (showShare ? "text-accent font-medium" : "text-muted")}>分享</button>
          <button onClick={onClose} className="ml-1 text-faint hover:text-ink">✕</button>
        </div>

        {showShare && art && <ShareBar art={art} onChange={(a) => setArt(a)} />}

        <div className="overflow-y-auto px-5 py-4">
          {ver ? <ArtifactRenderer blocks={ver.blocks} /> : <div className="text-[13px] text-faint">加载…</div>}
          {ver?.note && <div className="mt-4 border-t border-border pt-2 text-[11px] text-faint">本版更新: {ver.note}</div>}
        </div>
      </div>
    </div>
  );
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
