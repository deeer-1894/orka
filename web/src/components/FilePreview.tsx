import { useEffect, useState } from "react";
import { auth, files as fileApi, type FileVersion } from "../api";
import { lineDiff, diffStats } from "../lib/diff";
import { useOverlay } from "../lib/useOverlay";
import { Markdown } from "./Markdown";

// fetchText pulls a workspace file's text content (auth via Bearer header).
async function fetchText(path: string): Promise<string> {
  const r = await fetch(fileApi.downloadURL(path), { headers: { Authorization: "Bearer " + auth.token() } });
  if (!r.ok) throw new Error("HTTP " + r.status);
  return (await r.text()).slice(0, 200_000);
}

function fmtWhen(ms: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}:${String(d.getSeconds()).padStart(2, "0")}`;
}

// resolveWorkspaceImage maps a markdown image src to the workspace file API,
// relative to the directory of the .md file being previewed. Absolute/data/blob
// URLs are left untouched. Without this, ![](chart.png) loads from the page
// origin (localhost:5173/chart.png) and 404s.
export function resolveWorkspaceImage(mdPath: string, conv?: string) {
  const dir = mdPath.includes("/") ? mdPath.slice(0, mdPath.lastIndexOf("/") + 1) : "";
  return (src: string) => {
    if (/^([a-z]+:|\/\/|#)/i.test(src)) return src; // http(s):, data:, blob:, protocol-relative
    const rel = (dir + src.replace(/^\.\//, "")).replace(/^\//, "");
    return fileApi.previewURL(rel, conv);
  };
}

// FilePreview renders a workspace file inline by type: images as <img>, PDFs in
// an <iframe> (native browser viewer), text/markdown fetched and rendered, and
// any other binary (docx, xlsx, zip…) as a download card — never dumped as raw
// bytes, which is what produced the "乱码" for PDFs. Shared by the Files panel
// and the chat thread so a filename is clickable anywhere it appears.
export function FilePreview({ name, onClose, conv }: { name: string; onClose: () => void; conv?: string }) {
  const [content, setContent] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const [showHistory, setShowHistory] = useState(false);
  const [versions, setVersions] = useState<FileVersion[] | null>(null);
  const isImage = /\.(png|jpe?g|gif|webp|svg)$/i.test(name);
  const isPdf = /\.pdf$/i.test(name);
  const isMd = /\.(md|markdown)$/i.test(name);
  // Allowlist of extensions safe to show as text; anything else binary.
  const isText =
    isMd || /\.(txt|csv|tsv|json|ya?ml|xml|html?|css|js|ts|tsx|jsx|py|go|rs|java|c|cpp|h|sh|sql|toml|ini|conf|log|rtf)$/i.test(name);
  const url = fileApi.downloadURL(name, conv);
  // Version history reads the caller's own workspace — not meaningful for a file
  // viewed from someone else's shared conversation.
  const canHistory = !conv;
  useOverlay(onClose);

  useEffect(() => {
    if (!isText) return;
    fetch(url, { headers: { Authorization: "Bearer " + auth.token() } })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error("HTTP " + r.status))))
      .then((t) => setContent(t.slice(0, 100_000)))
      .catch((e) => setErr(String(e)));
  }, [url, isText]);

  // Lazy-load version history the first time the history view is opened.
  useEffect(() => {
    if (showHistory && versions === null) fileApi.versions(name).then(setVersions).catch(() => setVersions([]));
  }, [showHistory, versions, name]);

  const hasHistory = (versions?.length ?? 0) > 0;
  return (
    <div className="overlay-in fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
      <div
        className="pop-in flex max-h-[80vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-border bg-surface shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
          <span className="text-faint">📄</span>
          <span className="flex-1 truncate text-[14px] text-ink">{name}</span>
          {canHistory && (
            <button
              onClick={() => setShowHistory((v) => !v)}
              className={"text-[12px] hover:underline " + (showHistory ? "text-accent font-medium" : "text-muted")}
              title="版本历史"
            >
              🕘 历史{hasHistory ? ` · ${versions!.length}` : ""}
            </button>
          )}
          <a href={url} className="text-[12px] text-accent hover:underline">下载</a>
          <button onClick={onClose} className="ml-1 text-faint hover:text-ink">✕</button>
        </div>
        {showHistory ? (
          <FileHistory name={name} versions={versions} isText={isText} onRestored={() => { setVersions(null); setContent(null); setShowHistory(false); }} />
        ) : (
        <div className={isPdf ? "overflow-hidden" : "overflow-y-auto px-4 py-3"}>
          {isImage ? (
            <img src={url} alt={name} className="mx-auto max-w-full rounded" />
          ) : isPdf ? (
            <iframe src={fileApi.previewURL(name, conv)} title={name} className="h-[70vh] w-full border-0 bg-white" />
          ) : !isText ? (
            <div className="px-4 py-10 text-center">
              <div className="text-[34px]">📄</div>
              <div className="mt-2 text-[13px] text-muted">这是二进制文件,无法在此预览。</div>
              <a href={url} className="mt-3 inline-block rounded-lg bg-accentsoft px-3 py-1.5 text-[12.5px] text-accent hover:underline">
                下载 {name}
              </a>
            </div>
          ) : err ? (
            <div className="text-[13px] text-accent">无法预览:{err}</div>
          ) : content === null ? (
            <div className="text-[13px] text-faint">加载中…</div>
          ) : isMd ? (
            <Markdown resolveImage={resolveWorkspaceImage(name, conv)}>{content}</Markdown>
          ) : (
            <pre className="whitespace-pre-wrap break-words font-mono text-[12.5px] text-ink">{content}</pre>
          )}
        </div>
        )}
      </div>
    </div>
  );
}

// FileHistory lists prior versions of a file. Selecting one shows a line diff
// (current vs that version) for text files, with a one-click restore.
function FileHistory({
  name, versions, isText, onRestored,
}: { name: string; versions: FileVersion[] | null; isText: boolean; onRestored: () => void }) {
  const [sel, setSel] = useState<FileVersion | null>(null);
  const [diff, setDiff] = useState<ReturnType<typeof lineDiff> | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!sel || !isText) return setDiff(null);
    let alive = true;
    setDiff(null);
    Promise.all([fetchText(sel.path), fetchText(name).catch(() => "")])
      .then(([oldT, curT]) => alive && setDiff(lineDiff(oldT, curT)))
      .catch(() => alive && setDiff([]));
    return () => { alive = false; };
  }, [sel, isText, name]);

  const restore = async (v: FileVersion) => {
    setBusy(true);
    try {
      await fileApi.restore(name, v.ts);
      onRestored();
    } finally {
      setBusy(false);
    }
  };

  if (versions === null) return <div className="px-4 py-6 text-[13px] text-faint">加载历史…</div>;
  if (versions.length === 0)
    return <div className="px-4 py-8 text-center text-[13px] text-muted">暂无历史版本。文件被覆盖时会自动保存上一版。</div>;

  const stats = diff ? diffStats(diff) : null;
  return (
    <div className="flex min-h-0 flex-1">
      {/* version list */}
      <div className="w-48 shrink-0 overflow-y-auto border-r border-border py-2">
        <div className="px-3 pb-1 text-[11px] uppercase tracking-wide text-faint">版本 · {versions.length}</div>
        {versions.map((v) => (
          <button
            key={v.ts}
            onClick={() => setSel(v)}
            className={"flex w-full flex-col items-start gap-0.5 px-3 py-1.5 text-left hover:bg-surface2 " + (sel?.ts === v.ts ? "bg-accentsoft" : "")}
          >
            <span className="text-[12.5px] text-ink">{fmtWhen(v.when) || v.ts}</span>
            <span className="text-[11px] text-faint">{v.size} B</span>
          </button>
        ))}
      </div>
      {/* diff / actions */}
      <div className="flex min-w-0 flex-1 flex-col">
        {!sel ? (
          <div className="grid flex-1 place-items-center px-4 text-center text-[13px] text-faint">选择左侧某个版本查看差异并恢复</div>
        ) : (
          <>
            <div className="flex items-center gap-2 border-b border-border px-3 py-2 text-[12px]">
              <span className="text-muted">{fmtWhen(sel.when) || sel.ts} → 当前</span>
              {stats && <span className="text-ok">+{stats.add}</span>}
              {stats && <span className="text-accent">−{stats.del}</span>}
              <button
                onClick={() => restore(sel)}
                disabled={busy}
                className="ml-auto rounded-lg bg-accentsoft px-2.5 py-1 text-[12px] text-accent hover:underline disabled:opacity-50"
              >
                {busy ? "恢复中…" : "↩ 恢复此版本"}
              </button>
            </div>
            <div className="min-h-0 flex-1 overflow-auto px-1 py-1 font-mono text-[12px]">
              {!isText ? (
                <div className="px-3 py-6 text-center text-[12.5px] text-muted">
                  二进制文件不支持差异对比。
                  <a href={fileApi.downloadURL(sel.path)} className="ml-1 text-accent hover:underline">下载此版本</a>
                </div>
              ) : diff === null ? (
                <div className="px-3 py-4 text-[12.5px] text-faint">计算差异…</div>
              ) : diff.length === 0 ? (
                <div className="px-3 py-4 text-[12.5px] text-muted">两个版本内容相同。</div>
              ) : (
                diff.map((r, i) => (
                  <div
                    key={i}
                    className={
                      "whitespace-pre-wrap break-words px-2 " +
                      (r.type === "add" ? "bg-ok/10 text-ok" : r.type === "del" ? "bg-accent/10 text-accent" : "text-muted")
                    }
                  >
                    <span className="mr-2 select-none text-faint">{r.type === "add" ? "+" : r.type === "del" ? "−" : " "}</span>
                    {r.text || " "}
                  </div>
                ))
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
