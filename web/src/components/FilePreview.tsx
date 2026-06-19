import { useEffect, useState } from "react";
import { auth, files as fileApi } from "../api";
import { Markdown } from "./Markdown";

// resolveWorkspaceImage maps a markdown image src to the workspace file API,
// relative to the directory of the .md file being previewed. Absolute/data/blob
// URLs are left untouched. Without this, ![](chart.png) loads from the page
// origin (localhost:5173/chart.png) and 404s.
export function resolveWorkspaceImage(mdPath: string) {
  const dir = mdPath.includes("/") ? mdPath.slice(0, mdPath.lastIndexOf("/") + 1) : "";
  return (src: string) => {
    if (/^([a-z]+:|\/\/|#)/i.test(src)) return src; // http(s):, data:, blob:, protocol-relative
    const rel = (dir + src.replace(/^\.\//, "")).replace(/^\//, "");
    return fileApi.previewURL(rel);
  };
}

// FilePreview renders a workspace file inline by type: images as <img>, PDFs in
// an <iframe> (native browser viewer), text/markdown fetched and rendered, and
// any other binary (docx, xlsx, zip…) as a download card — never dumped as raw
// bytes, which is what produced the "乱码" for PDFs. Shared by the Files panel
// and the chat thread so a filename is clickable anywhere it appears.
export function FilePreview({ name, onClose }: { name: string; onClose: () => void }) {
  const [content, setContent] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const isImage = /\.(png|jpe?g|gif|webp|svg)$/i.test(name);
  const isPdf = /\.pdf$/i.test(name);
  const isMd = /\.(md|markdown)$/i.test(name);
  // Allowlist of extensions safe to show as text; anything else binary.
  const isText =
    isMd || /\.(txt|csv|tsv|json|ya?ml|xml|html?|css|js|ts|tsx|jsx|py|go|rs|java|c|cpp|h|sh|sql|toml|ini|conf|log|rtf)$/i.test(name);
  const url = fileApi.downloadURL(name);

  useEffect(() => {
    if (!isText) return;
    fetch(url, { headers: { Authorization: "Bearer " + auth.token() } })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error("HTTP " + r.status))))
      .then((t) => setContent(t.slice(0, 100_000)))
      .catch((e) => setErr(String(e)));
  }, [url, isText]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
      <div
        className="flex max-h-[80vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-border bg-surface shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
          <span className="text-faint">📄</span>
          <span className="flex-1 truncate text-[14px] text-ink">{name}</span>
          <a href={url} className="text-[12px] text-accent hover:underline">下载</a>
          <button onClick={onClose} className="ml-1 text-faint hover:text-ink">✕</button>
        </div>
        <div className={isPdf ? "overflow-hidden" : "overflow-y-auto px-4 py-3"}>
          {isImage ? (
            <img src={url} alt={name} className="mx-auto max-w-full rounded" />
          ) : isPdf ? (
            <iframe src={fileApi.previewURL(name)} title={name} className="h-[70vh] w-full border-0 bg-white" />
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
            <Markdown resolveImage={resolveWorkspaceImage(name)}>{content}</Markdown>
          ) : (
            <pre className="whitespace-pre-wrap break-words font-mono text-[12.5px] text-ink">{content}</pre>
          )}
        </div>
      </div>
    </div>
  );
}
