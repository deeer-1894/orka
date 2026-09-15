import { ActionChip } from './ActionChip';
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { files as fileApi, type FileEntry } from "../api";
import { invalidateSessionFiles, useFileRevision } from "../hooks/useFileRevision";
import { Icon } from "./Icon";
import { FilePreview } from "./FilePreview";
import { PanelEmpty as Blank } from "./PanelEmpty";
const DeliverySnapshots = lazy(() => import("./DeliverySnapshots"));
// File "kinds" group the flat workspace into tidy, labelled sections so a busy
// workspace reads as 文档 / 代码 / 数据 / 图片 / 其他 instead of one long dump.
const FILE_KINDS: { id: string; icon: string; label: string; color: string; exts?: string[] }[] = [
  { id: "folder", icon: "📁", label: "文件夹", color: "text-[#c79a5a]" },
  { id: "doc", icon: "📝", label: "文档 / 报告", color: "text-[#5a86c7]", exts: ["md", "markdown", "txt", "doc", "docx", "rtf"] },
  { id: "pdf", icon: "📕", label: "PDF", color: "text-[#d06363]", exts: ["pdf"] },
  { id: "code", icon: "🧑‍💻", label: "代码", color: "text-[#7a9a6a]", exts: ["py", "js", "jsx", "ts", "tsx", "go", "java", "c", "h", "cpp", "rs", "sh", "rb", "php", "html", "css", "yaml", "yml", "sql"] },
  { id: "data", icon: "📊", label: "数据 / 表格", color: "text-[#5aa48a]", exts: ["json", "csv", "tsv", "xml", "ndjson", "xlsx", "xls"] },
  { id: "image", icon: "🖼️", label: "图片", color: "text-[#b07ac7]", exts: ["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp"] },
  { id: "slides", icon: "📑", label: "演示", color: "text-[#c78a5a]", exts: ["pptx", "ppt", "key"] },
  { id: "other", icon: "🗂️", label: "其他", color: "text-faint" },
];
function kindOf(name: string, dir: boolean): string {
  if (dir) return "folder";
  const ext = name.split(".").pop()?.toLowerCase() || "";
  for (const k of FILE_KINDS) if (k.exts?.includes(ext)) return k.id;
  return "other";
}
function kindMeta(name: string, dir: boolean) {
  const id = kindOf(name, dir);
  return FILE_KINDS.find((k) => k.id === id)!;
}

// dupKey normalizes a filename to spot likely-duplicate artifacts the agent may
// have generated several times: drop the extension, lowercase, strip separators
// and trailing language/version/date suffixes so e.g. "Report_EN" /
// "report-en-v2" / "ai_agent_research" / "ai-agent-research" collapse together.
function dupKey(name: string, dir: boolean): string {
  let s = dir ? name : name.replace(/\.[^.]+$/, "");
  s = s.toLowerCase().replace(/[\s_\-.]+/g, "");
  s = s.replace(/(business|analysis|report|经营分析|报告|en|zh|cn|final|copy|v?\d{1,4}|\d{6,8})/g, "");
  return s;
}

type FileItem = FileEntry;
type SortKey = "name" | "time" | "size";

// Runtime junk the sandbox leaves in the workspace (HOME=root → caches, python
// bytecode) — hidden by default so the panel shows the user's actual files.
const JUNK = new Set(["__pycache__", "Library", "node_modules", ".cache", ".config", ".local", ".orka_trash", ".npm", ".ipynb_checkpoints"]);
function isHidden(name: string): boolean {
  return name.startsWith(".") || JUNK.has(name);
}

export function FilesPanel({ email, conversationID }: { email: string; conversationID: string }) {
  const [deliveriesOpen, setDeliveriesOpen] = useState(false);
  const fileRevision = useFileRevision(conversationID);
  const [items, setItems] = useState<FileItem[]>([]);
  const [pct, setPct] = useState<number | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [showHidden, setShowHidden] = useState(false);
  const [sort, setSort] = useState<SortKey>("name");
  const [dir, setDir] = useState(".");
  const inputRef = useRef<HTMLInputElement>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  useEffect(() => {
    let current = true;
    setItems([]); setError(""); setLoading(!!conversationID);
    if (conversationID) fileApi.scopedList(dir, conversationID, true)
      .then(items => { if (current) setItems(items); })
      .catch(e => { if (current) setError(String(e)); })
      .finally(() => { if (current) setLoading(false); });
    return () => { current = false; };
  }, [conversationID, dir, revision, fileRevision]);
  const navigate = (path: string) => { setItems([]); setQuery(""); setPreview(null); setDir(path); };
  const fullPath = (name: string) => dir === "." ? name : `${dir}/${name}`;
  const onUpload = async (f: File) => {
    if (!conversationID || pct !== null) return;
    setPct(0);
    try {
      await fileApi.upload(f, dir === "." ? "" : dir, n => { if (alive.current) setPct(n); }, conversationID);
      if (alive.current) { setRevision(n => n + 1); invalidateSessionFiles(conversationID); }
    } catch (e) {
      if (alive.current) setError(String(e));
    } finally {
      if (alive.current) { setPct(null); if (inputRef.current) inputRef.current.value = ""; }
    }
  };
  const del = async (name: string) => {
    if (!conversationID) return;
    try {
      await fileApi.delete(fullPath(name), conversationID);
      if (alive.current) { setRevision(n => n + 1); invalidateSessionFiles(conversationID); }
    } catch (e) { if (alive.current) setError(String(e)); }
  };

  const q = query.trim().toLowerCase();
  const hiddenCount = items.filter((it) => isHidden(it.name)).length;
  const cmp =
    sort === "time"
      ? (a: FileItem, b: FileItem) => (b.mtime || 0) - (a.mtime || 0)
      : sort === "size"
        ? (a: FileItem, b: FileItem) => b.size - a.size
        : (a: FileItem, b: FileItem) => a.name.localeCompare(b.name);
  const filtered = items
    .filter((it) => showHidden || !isHidden(it.name))
    .filter((it) => !q || it.name.toLowerCase().includes(q))
    .sort(cmp);
  const grouped = FILE_KINDS.map((k) => ({ ...k, files: filtered.filter((it) => kindOf(it.name, it.dir) === k.id) })).filter(
    (g) => g.files.length > 0,
  );

  // Flag likely-duplicate files (same normalized stem) so a workspace full of
  // near-identical agent exports is legible at a glance.
  const dupSiblings = new Map<string, string[]>();
  {
    const byKey = new Map<string, string[]>();
    for (const it of filtered) {
      const k = dupKey(it.name, it.dir);
      if (k.length < 3) continue;
      (byKey.get(k) ?? byKey.set(k, []).get(k)!).push(it.name);
    }
    for (const names of byKey.values()) if (names.length > 1) for (const n of names) dupSiblings.set(n, names.filter((x) => x !== n));
  }

  const Row = (it: FileItem) => {
    const meta = kindMeta(it.name, it.dir);
    const dups = dupSiblings.get(it.name);
    return (
    <div key={it.name} className="group flex items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface2">
      <span className={meta.color}>{meta.icon}</span>
      {it.dir ? (
        <button onClick={() => navigate(fullPath(it.name))} className="min-w-0 flex-1 whitespace-normal break-all text-left text-[14px] text-ink hover:text-accent">{it.name}</button>
      ) : (
        <button onClick={() => setPreview(fullPath(it.name))} className="min-w-0 flex-1 whitespace-normal break-all text-left text-[14px] text-ink hover:text-accent" title="预览">
          {it.name}
        </button>
      )}
      {dups && dups.length > 0 && (
        <span className="shrink-0 text-[11px] text-[#c79a5a] group-hover:hidden" title={"可能与以下文件重复:\n" + dups.join("\n")}>🔁</span>
      )}
      <span className="text-[11px] text-faint">{fmtBytes(it.size)}</span>
      {!it.dir && (
        <a href={fileApi.downloadURL(fullPath(it.name), conversationID)} className="text-accent opacity-0 group-hover:opacity-100" aria-label={"下载 " + it.name}>
          <Icon name="download" size={13} />
        </a>
      )}
      <button onClick={() => del(it.name)} className="text-faint opacity-0 hover:text-accent group-hover:opacity-100" aria-label={"删除 " + it.name}>
        <Icon name="trash" size={13} />
      </button>
    </div>
    );
  };

  return (
    <div className="p-3">
      {conversationID && <><ActionChip aria-expanded={deliveriesOpen} onClick={() => setDeliveriesOpen(value => !value)} className="mb-3" icon="file">交付快照</ActionChip>
      {deliveriesOpen && <Suspense fallback={<p role="status">正在加载快照…</p>}><DeliverySnapshots key={conversationID} conversationID={conversationID} /></Suspense>}</>}
      <h3 className="mb-2 text-sm font-medium">当前工作区</h3>
      <div className="mb-2 flex items-center justify-between gap-2">
        <nav aria-label="文件夹路径" className="flex min-w-0 flex-wrap items-center gap-1 text-[12px] text-faint" title={email}>
          <Icon name="folder" size={13} />
          <button onClick={() => navigate(".")} className="hover:text-accent">本会话文件</button>
          {dir !== "." && dir.split("/").map((part, i, parts) => <span key={i} className="inline-flex min-w-0 items-center gap-1"> / <button className="truncate hover:text-accent" onClick={() => navigate(parts.slice(0, i + 1).join("/"))}>{part}</button></span>)}
        </nav>
        <button
          onClick={() => inputRef.current?.click()}
          disabled={!conversationID || pct !== null}
          className="shrink-0 rounded-lg border border-border px-2.5 py-1 text-[12px] text-muted hover:border-accent/40"
        >
          上传
        </button>
        <input ref={inputRef} type="file" hidden onChange={(e) => e.target.files?.[0] && onUpload(e.target.files[0])} />
      </div>
      {dir !== "." && <button onClick={() => navigate(dir.includes("/") ? dir.slice(0, dir.lastIndexOf("/")) : ".")} className="mb-2 text-[12px] text-muted hover:text-accent">← 返回上级</button>}
      {error && <div role="alert" className="mb-2 text-[12px] text-accent">无法读取文件：{error} <button onClick={() => setRevision(n => n + 1)} className="underline">重试</button></div>}
      {loading && <p role="status" className="py-4 text-[13px] text-faint">加载中…</p>}
      {items.length > 6 && (
        <div className="mb-2 flex items-center gap-1.5">
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="筛选文件…"
            className="min-w-0 flex-1 rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[12.5px] outline-none focus:border-accent/50"
          />
          <select
            value={sort}
            onChange={(e) => setSort(e.target.value as SortKey)}
            className="shrink-0 rounded-lg border border-border bg-surface px-1.5 py-1.5 text-[12px] text-muted outline-none"
            title="排序方式"
          >
            <option value="name">名称</option>
            <option value="time">时间</option>
            <option value="size">大小</option>
          </select>
        </div>
      )}
      {hiddenCount > 0 && (
        <button
          onClick={() => setShowHidden((v) => !v)}
          className="mb-2 text-[11.5px] text-faint hover:text-accent"
          title="系统缓存 / 隐藏文件(__pycache__、.cache 等)"
        >
          {showHidden ? "隐藏" : "显示"}系统文件 · {hiddenCount}
        </button>
      )}
      {pct !== null && (
        <div className="mb-2 h-1 w-full overflow-hidden rounded bg-surface2">
          <div className="h-full bg-accent transition-all" style={{ width: pct + "%" }} />
        </div>
      )}
      {!loading && !error && filtered.length === 0 && (
        !conversationID ? <Blank icon="folder" title="请选择会话">选择或创建会话后查看文件。</Blank> : items.length === 0
          ? <Blank icon="folder" title="工作区还是空的">Orka 产出的文件(报告、图表、脚本、导出的文档)都会落在这里,你也可以直接上传文件让它读取。</Blank>
          : <Blank icon="search" title="没有匹配的文件">换个关键词试试,或清空搜索框查看全部。</Blank>
      )}
      {grouped.map((g) => (
        <div key={g.id} className="mb-3">
          <div className="mb-1 px-1 text-[11px] font-medium uppercase tracking-wide text-faint">
            {g.icon} {g.label} · {g.files.length}
          </div>
          <div className="space-y-0.5">{g.files.map(Row)}</div>
        </div>
      ))}
      {preview && <FilePreview name={preview} conv={conversationID} onClose={() => setPreview(null)} />}
    </div>
  );
}

function fmtBytes(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1).replace(/\.0$/, "") + " KB";
  return (n / (1024 * 1024)).toFixed(1).replace(/\.0$/, "") + " MB";
}
