import { HtmlPreview } from "./HtmlPreview";
import { HTML_PREVIEW_MAX_BYTES } from "../lib/htmlPreview";
import { ActionChip } from "./ActionChip";
import { lazy, Suspense, useEffect, useMemo, useState } from "react";
import { auth, files as fileApi, type FileVersion } from "../api";
import { lineDiff, diffStats } from "../lib/diff";
import { useOverlay } from "../lib/useOverlay";
import { Markdown } from "./Markdown";
import { CSV_MAX_COLUMNS, CSV_MAX_ROWS, PREVIEW_MAX_BYTES, highlightCode, languageForFile, parseDelimited, readBoundedText } from "../lib/filePreview";

const XlsxPreview = lazy(() => import("./XlsxPreview"));

// fetchText pulls a workspace file's text content (auth via Bearer header).
async function fetchText(path: string, conv: string) {
  const r = await fetch(fileApi.downloadURL(path, conv), { headers: { Authorization: "Bearer " + auth.token() } });
  return readBoundedText(r);
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
export function resolveWorkspaceImage(mdPath: string, conv: string) {
  const dir = mdPath.includes("/") ? mdPath.slice(0, mdPath.lastIndexOf("/") + 1) : "";
  return (src: string) => {
    if (/^([a-z]+:|\/\/|#)/i.test(src)) return src; // http(s):, data:, blob:, protocol-relative
    const rel = (dir + src.replace(/^\.\//, "")).replace(/^\//, "");
    return fileApi.previewURL(rel, conv);
  };
}

// FilePreview renders a workspace file inline by type: images as <img>, PDFs in
// an <iframe> (native browser viewer), text/markdown fetched and rendered, and
// other unsupported binary files (zip…) as a download card — never dumped as raw
// bytes, which is what produced the "乱码" for PDFs. Shared by the Files panel
// and the chat thread so a filename is clickable anywhere it appears.
export function FilePreview(props: { name: string; onClose: () => void; conv: string; readOnly?: boolean; initialHistory?: boolean }) {
  return <FilePreviewContent key={props.conv + ":" + props.name} {...props} />;
}

function FilePreviewContent({ name, onClose, conv, readOnly = false, initialHistory }: { name: string; onClose: () => void; conv: string; readOnly?: boolean; initialHistory?: boolean }) {
  const [content, setContent] = useState<string | null>(null);
  const [truncated, setTruncated] = useState(false);
  const [htmlMode, setHtmlMode] = useState<"preview" | "source">("preview");
  const [revision, setRevision] = useState(0);
  const [err, setErr] = useState("");
  const [showHistory, setShowHistory] = useState(!!initialHistory && !readOnly);
  const [versions, setVersions] = useState<FileVersion[] | null>(null);
  const isImage = /\.(png|jpe?g|gif|webp|svg)$/i.test(name);
  const isXlsx = /\.xlsx$/i.test(name);
  const isPdf = /\.pdf$/i.test(name);
  const isHtml = /\.html?$/i.test(name);
  const maxBytes = isHtml ? HTML_PREVIEW_MAX_BYTES : PREVIEW_MAX_BYTES;
  const isMd = /\.(md|markdown)$/i.test(name);
  // Allowlist of extensions safe to show as text; anything else binary.
  const isText =
    isMd || !!languageForFile(name) || /\.(txt|csv|tsv|log|rtf)$/i.test(name);
  const isCsv = /\.(csv|tsv)$/i.test(name);
  const url = fileApi.downloadURL(name, conv);
  const canHistory = !readOnly;
  useOverlay(onClose);

  useEffect(() => {
    if (!isText) return;
    const controller = new AbortController();
    setContent(null); setErr(""); setTruncated(false);
    fetch(url, { headers: { Authorization: "Bearer " + auth.token() }, signal: controller.signal })
      .then(response => readBoundedText(response, maxBytes))
      .then((result) => { if (!controller.signal.aborted) { setContent(result.text); setTruncated(result.truncated); } })
      .catch((e) => { if (!controller.signal.aborted) setErr(String(e)); });
    return () => controller.abort();
  }, [url, isText, revision, maxBytes]);

  useEffect(() => {
    let alive = true;
    if (canHistory && showHistory && versions === null) fileApi.versions(name, conv)
      .then(v => { if (alive) setVersions(v); }).catch(() => { if (alive) setVersions([]); });
    return () => { alive = false; };
  }, [showHistory, versions, name, conv, canHistory]);

  const hasHistory = (versions?.length ?? 0) > 0;
  return (
    <div className="overlay-in fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-6" onClick={onClose}>
      <div
        role="dialog" aria-modal="true" aria-label={"预览 " + name}
        className={"pop-in flex max-h-[90vh] w-full flex-col overflow-hidden rounded-2xl border border-border bg-surface shadow-xl " + (isHtml ? "max-w-6xl" : "max-w-2xl")}
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
          <button onClick={onClose} aria-label="关闭预览" className="ml-1 text-faint hover:text-ink">✕</button>
        </div>
        {isHtml && !showHistory && <div className="flex items-center gap-2 border-b border-border px-4 py-2" role="group" aria-label="HTML 视图">
          <ActionChip icon="eye" aria-pressed={htmlMode === "preview"} onClick={() => setHtmlMode("preview")}>页面预览</ActionChip>
          <ActionChip icon="code" aria-pressed={htmlMode === "source"} onClick={() => setHtmlMode("source")}>源码</ActionChip>
        </div>}
        {showHistory ? (
          <FileHistory name={name} versions={versions} isText={isText} conv={conv} onRestored={() => { setVersions(null); setContent(null); setRevision(n => n + 1); setShowHistory(false); }} />
        ) : (
        <div className={isPdf || (isHtml && htmlMode === "preview") ? "overflow-auto" : "overflow-y-auto px-4 py-3"}>
          {isXlsx ? <Suspense fallback={<p role="status">正在加载表格预览…</p>}><XlsxPreview url={url} /></Suspense> : isImage ? (
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
          ) : isHtml && htmlMode === "preview" ? (
            truncated ? <p role="status" className="p-4 text-sm text-muted">HTML 超出页面预览大小，请下载完整文件查看，或切换源码。</p> : <HtmlPreview text={content} name={name} conv={conv} />
          ) : isCsv ? (
            <CsvPreview text={content} delimiter={/\.tsv$/i.test(name) ? "\t" : ","} />
          ) : isMd ? (
            <Markdown resolveImage={resolveWorkspaceImage(name, conv)}>{content}</Markdown>
          ) : (
            <CodePreview text={content} name={name} />
          )}
          {truncated && <p role="status" className="mt-2 text-[12px] text-muted">预览已截断：最多读取 {maxBytes.toLocaleString()} 字节。下载查看完整文件。</p>}
        </div>
        )}
      </div>
    </div>
  );
}

// FileHistory lists prior versions of a file. Selecting one shows a line diff
// (current vs that version) for text files, with a one-click restore.
function FileHistory({
  name, versions, isText, conv, onRestored,
}: { name: string; versions: FileVersion[] | null; isText: boolean; conv: string; onRestored: () => void }) {
  const [sel, setSel] = useState<FileVersion | null>(null);
  const [diff, setDiff] = useState<ReturnType<typeof lineDiff> | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [diffTruncated, setDiffTruncated] = useState(false);

  useEffect(() => {
    if (!sel || !isText) return setDiff(null);
    let alive = true;
    setDiff(null); setDiffTruncated(false);
    Promise.all([fetchText(sel.path, conv), fetchText(name, conv)])
      .then(([oldT, curT]) => {
        if (!alive) return;
        // LCS is quadratic in lines; a byte limit alone cannot bound its work.
        const oldLines = oldT.text.split("\n", 501), curLines = curT.text.split("\n", 501);
        setDiffTruncated(oldT.truncated || curT.truncated || oldLines.length > 500 || curLines.length > 500);
        setDiff(lineDiff(oldLines.slice(0, 500).join("\n"), curLines.slice(0, 500).join("\n")));
      })
      .catch(() => { if (alive) { setError("无法读取版本内容"); setDiff([]); } });
    return () => { alive = false; };
  }, [sel, isText, name, conv]);

  const restore = async (v: FileVersion) => {
    setBusy(true); setError("");
    try {
      await fileApi.restore(name, v.ts, conv);
      onRestored();
    } catch (e) {
      setError(String(e));
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
        {error && <p role="alert" className="p-3 text-[12px] text-accent">{error}</p>}
        {diffTruncated && <p role="status" className="p-3 text-[12px] text-muted">差异预览已截断：每个版本最多 500 行、100,000 字节。</p>}
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
                  <a href={fileApi.downloadURL(sel.path, conv)} className="ml-1 text-accent hover:underline">下载此版本</a>
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

export function CsvPreview({ text, delimiter = "," }: { text: string; delimiter?: string }) {
  const result = useMemo(() => parseDelimited(text, delimiter), [text, delimiter]);
  const columns = Math.max(0, ...result.rows.map(row => row.length));
  if (!result.rows.length) return <p className="text-[13px] text-muted">空文件</p>;
  return <div>
    <div className="max-h-[55vh] overflow-auto rounded-lg border border-border" role="region" aria-label="CSV 表格" tabIndex={0}>
      <table className="w-full border-collapse text-left font-mono text-[12px]">
        <thead className="sticky top-0 bg-surface2"><tr>{Array.from({ length: columns }, (_, i) => <th key={i} scope="col" className="border-b border-border px-3 py-2 font-semibold"><div className="max-w-xs whitespace-pre-wrap break-words">{result.rows[0][i] ?? `列 ${i + 1}`}</div></th>)}</tr></thead>
        <tbody>{result.rows.slice(1).map((row, r) => <tr key={r} className="even:bg-surface2/40">{Array.from({ length: columns }, (_, c) => <td key={c} className="border-b border-border px-3 py-2 align-top"><div className="max-w-xs whitespace-pre-wrap break-words">{row[c] ?? ""}</div></td>)}</tr>)}</tbody>
      </table>
    </div>
    {result.truncated && <p role="status" className="mt-2 text-[12px] text-muted">表格预览已截断：最多 {CSV_MAX_ROWS} 行数据、{CSV_MAX_COLUMNS} 列。下载查看完整文件。</p>}
    {result.incomplete && <p role="status" className="mt-2 text-[12px] text-muted">CSV 引号未闭合或格式不完整，已显示可读取的内容。</p>}
  </div>;
}

const TOKEN_CLASS = { comment: "text-faint italic", string: "text-ok", number: "text-accent", keyword: "text-accent font-semibold", tag: "text-accent" };
export function CodePreview({ text, name }: { text: string; name: string }) {
  const language = languageForFile(name);
  const tokens = useMemo(() => highlightCode(text, language), [text, language]);
  return <div>
    {language && <div className="mb-1 text-[11px] text-faint">{language}</div>}
    <pre className="overflow-auto rounded-lg bg-surface2/40 p-3 font-mono text-[12.5px] text-ink"><code>{tokens.map((token, i) => token.kind ? <span key={i} className={TOKEN_CLASS[token.kind]}>{token.text}</span> : token.text)}</code></pre>
  </div>;
}
