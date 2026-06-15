import { useEffect, useRef, useState } from "react";
import type { RunStatus } from "../hooks/useChatStream";
import { TOOL_GROUPS } from "../lib/toolGroups";
import { api, files as fileApi } from "../api";
import { toast, toastError } from "../lib/toast";

// Icons/labels for the built-in skills; installed/custom skills get a default.
const SKILL_ICON: Record<string, { icon: string; label: string }> = {
  researcher: { icon: "🔎", label: "调研" },
  writer: { icon: "✍️", label: "写作" },
  coder: { icon: "💻", label: "编程" },
  analyst: { icon: "📊", label: "分析" },
  translator: { icon: "🌐", label: "翻译" },
};

interface Attachment { name: string; path: string; image: boolean }
const IMAGE_RE = /\.(png|jpe?g|gif|webp|bmp)$/i;

// Mirrors the built-in skills registered in the control layer (skills_registry.go).
// Selecting one prepends a directive the model honours via apply_skill.
export const SKILLS = [
  { name: "researcher", label: "调研", icon: "🔎", desc: "多来源交叉验证 + 引用" },
  { name: "writer", label: "写作", icon: "✍️", desc: "结构化专业文案" },
  { name: "coder", label: "编程", icon: "💻", desc: "可运行代码 + 设计权衡" },
  { name: "analyst", label: "分析", icon: "📊", desc: "结构化拆解 + 建议" },
  { name: "translator", label: "翻译", icon: "🌐", desc: "自然地道的翻译" },
];

export function Composer({
  status,
  onSend,
  onKill,
  enabledGroups,
  onToggleGroup,
  onClearGroups,
  activeSkill,
  onPickSkill,
}: {
  status: RunStatus;
  onSend: (msg: string, fileIDs?: string[]) => void;
  onKill: () => void;
  enabledGroups: Set<string>;
  onToggleGroup: (id: string) => void;
  onClearGroups: () => void;
  activeSkill: string | null;
  onPickSkill: (name: string | null) => void;
}) {
  const [text, setText] = useState("");
  const [menu, setMenu] = useState(false);
  const [toolsOpen, setToolsOpen] = useState(false);
  const [skills, setSkills] = useState<{ name: string; description: string }[]>([]);
  const [installUrl, setInstallUrl] = useState("");
  const [installing, setInstalling] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [previewName, setPreviewName] = useState<string | null>(null);
  const [previewCache, setPreviewCache] = useState<Record<string, string>>({});
  // @-mention: reference a workspace file as context (Cursor-style).
  const [atOpen, setAtOpen] = useState(false);
  const [atQuery, setAtQuery] = useState("");
  const [atSel, setAtSel] = useState(0);
  const [atFiles, setAtFiles] = useState<{ name: string; dir: boolean; size: number }[]>([]);

  const togglePreview = async (name: string) => {
    if (previewName === name) { setPreviewName(null); return; }
    setPreviewName(name);
    if (previewCache[name] === undefined) {
      try {
        const r = await api.getSkill(name);
        setPreviewCache((c) => ({ ...c, [name]: r.prompt || "(空)" }));
      } catch {
        setPreviewCache((c) => ({ ...c, [name]: "(加载失败)" }));
      }
    }
  };

  const menuRef = useRef<HTMLDivElement>(null);

  const loadSkills = () => api.listSkills().then((r) => setSkills(r.skills || [])).catch(() => {});
  useEffect(() => { loadSkills(); }, []);
  useEffect(() => { if (menu) loadSkills(); }, [menu]);

  // Close the skill menu on an outside click or Escape (the textarea's own
  // Escape only fires while it's focused — this covers clicks anywhere else).
  useEffect(() => {
    if (!menu) return;
    const onDown = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node) && taRef.current !== e.target) {
        setMenu(false);
      }
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setMenu(false); };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => { document.removeEventListener("mousedown", onDown); document.removeEventListener("keydown", onKey); };
  }, [menu]);

  const installSkill = async () => {
    const url = installUrl.trim();
    if (!url) return;
    setInstalling(true);
    try {
      const r = await api.installSkill(url);
      toast(`已安装技能「${r.name}」`, "success");
      setInstallUrl("");
      loadSkills();
    } catch {
      toastError("安装失败：请确认这是一个可访问的 SKILL.md 链接");
    } finally {
      setInstalling(false);
    }
  };
  const removeSkill = async (name: string) => {
    setDeleting(name);
    try {
      await api.deleteSkill(name);
      toast(`已删除技能「${name}」`, "success");
      if (activeSkill === name) onPickSkill(null);
      loadSkills();
    } catch {
      toastError("删除失败");
    } finally {
      setDeleting(null);
    }
  };
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [uploading, setUploading] = useState(0);
  const [dragging, setDragging] = useState(false);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const busy = status === "streaming";

  // Auto-grow the textarea up to a cap (every major agent input does this; a
  // fixed 1-row box cramps multi-line prompts).
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = Math.min(ta.scrollHeight, 200) + "px";
  }, [text]);

  const onFiles = async (list: FileList | null) => {
    if (!list?.length) return;
    setUploading((n) => n + list.length);
    for (const f of Array.from(list)) {
      try {
        const path = await fileApi.upload(f, "");
        setAttachments((a) => [...a, { name: f.name, path, image: IMAGE_RE.test(f.name) }]);
      } catch {
        toastError("上传失败：" + f.name);
      } finally {
        setUploading((n) => n - 1);
      }
    }
    if (fileRef.current) fileRef.current.value = "";
  };

  // Paste an image straight from the clipboard (e.g. a screenshot) → upload it.
  const onPaste = (e: React.ClipboardEvent) => {
    const imgs = Array.from(e.clipboardData.files).filter((f) => f.type.startsWith("image/"));
    if (imgs.length) {
      e.preventDefault();
      const dt = new DataTransfer();
      imgs.forEach((f) => dt.items.add(f));
      onFiles(dt.files);
    }
  };

  // Drag a file anywhere onto the composer to upload it.
  const onDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragging(false);
    if (e.dataTransfer.files?.length) onFiles(e.dataTransfer.files);
  };

  // "/" at the start of an empty box opens the skill menu.
  useEffect(() => {
    if (text === "/") setMenu(true);
  }, [text]);

  // Lazy-load workspace files the first time an @-mention opens.
  useEffect(() => {
    if (atOpen && atFiles.length === 0) fileApi.list(".").then(setAtFiles).catch(() => {});
  }, [atOpen, atFiles.length]);

  // Detect an `@token` being typed at the cursor → open the file picker.
  const syncAt = (value: string, cursor: number | null) => {
    const before = value.slice(0, cursor ?? value.length);
    const m = before.match(/(^|\s)@([^\s@]*)$/);
    if (m) { setAtOpen(true); setAtQuery(m[2]); setAtSel(0); } else setAtOpen(false);
  };
  const atMatches = atFiles
    .filter((f) => !f.dir && f.name.toLowerCase().includes(atQuery.toLowerCase()))
    .slice(0, 8);

  // Insert the picked file as a context attachment and strip the @token.
  const pickFile = (name: string) => {
    const ta = taRef.current;
    const cursor = ta?.selectionStart ?? text.length;
    const before = text.slice(0, cursor).replace(/(^|\s)@([^\s@]*)$/, (_full, pre) => pre);
    setText(before + text.slice(cursor));
    setAtOpen(false);
    setAttachments((a) => (a.some((x) => x.path === name) ? a : [...a, { name, path: name, image: IMAGE_RE.test(name) }]));
    requestAnimationFrame(() => ta?.focus());
  };

  const send = () => {
    if ((!text.trim() && attachments.length === 0) || busy || uploading > 0) return;
    onSend(text.trim(), attachments.map((a) => a.path));
    setText("");
    setAttachments([]);
    setMenu(false);
  };

  // Skills are now a locked "mode" (structured active_skill the backend injects
  // deterministically), not a one-shot text macro that pollutes the input.
  const pickSkill = (name: string) => {
    setMenu(false);
    onPickSkill(name);
    // strip the leading "/…" trigger if present, keep the user's actual text
    setText((t) => t.replace(/^\/\w*\s*/, ""));
    requestAnimationFrame(() => taRef.current?.focus());
  };
  const skill = activeSkill ? { icon: SKILL_ICON[activeSkill]?.icon || "🧩", label: SKILL_ICON[activeSkill]?.label || activeSkill } : null;

  return (
    <div className="px-5 pb-5">
      <div className="relative mx-auto max-w-3xl">
        {menu && (
          <div ref={menuRef} className="absolute bottom-[calc(100%+8px)] left-0 z-20 w-80 rounded-2xl border border-border bg-surface p-1.5 shadow-lg">
            <div className="px-2 py-1 text-[11px] font-medium uppercase tracking-wide text-faint">
              技能 · 让 Orka 切换专长
            </div>
            <div className="max-h-[40vh] overflow-y-auto">
              {skills.map((s) => {
                const meta = SKILL_ICON[s.name];
                const builtin = !!meta;
                return (
                  <div key={s.name}>
                    <div className="group flex items-center rounded-lg hover:bg-surface2">
                      <button
                        onClick={() => pickSkill(s.name)}
                        className="flex min-w-0 flex-1 items-center gap-2.5 rounded-lg px-2 py-1.5 text-left"
                      >
                        <span className="text-[18px]">{meta?.icon || "🧩"}</span>
                        <span className="min-w-0">
                          <span className="block text-[13px] text-ink">
                            {meta?.label || s.name} <span className="text-faint">/{s.name}</span>
                            {!builtin && <span className="ml-1 rounded bg-surface2 px-1 text-[10px] text-faint">已安装</span>}
                          </span>
                          <span className="block truncate text-[12px] text-muted">{s.description}</span>
                        </span>
                      </button>
                      <button
                        onClick={() => togglePreview(s.name)}
                        title="查看技能内容"
                        aria-label={"查看技能 " + s.name + " 的内容"}
                        aria-expanded={previewName === s.name}
                        className={
                          "shrink-0 rounded-md px-1.5 py-1 text-[13px] transition group-hover:opacity-100 " +
                          (previewName === s.name ? "text-accent opacity-100" : "text-faint opacity-0 hover:bg-surface hover:text-ink")
                        }
                      >
                        {previewName === s.name ? "▴" : "👁"}
                      </button>
                      {!builtin && (
                        <button
                          onClick={() => removeSkill(s.name)}
                          disabled={deleting === s.name}
                          title={"删除技能 " + s.name}
                          aria-label={"删除技能 " + s.name}
                          className="mr-1 shrink-0 rounded-md px-1.5 py-1 text-[13px] text-faint opacity-0 hover:bg-surface hover:text-red-500 group-hover:opacity-100 disabled:opacity-40"
                        >
                          {deleting === s.name ? "…" : "🗑"}
                        </button>
                      )}
                    </div>
                    {previewName === s.name && (
                      <div className="mx-2 mb-1 max-h-40 overflow-y-auto whitespace-pre-wrap rounded-lg border border-border bg-surface2/50 px-2.5 py-2 text-[11.5px] leading-relaxed text-muted">
                        {previewCache[s.name] ?? "加载中…"}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
            <div className="mt-1 border-t border-border px-1 pt-1.5">
              <div className="mb-1 px-1 text-[11px] text-faint">从网上安装技能 (SKILL.md 链接)</div>
              <div className="flex items-center gap-1">
                <input
                  value={installUrl}
                  onChange={(e) => setInstallUrl(e.target.value)}
                  onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); installSkill(); } }}
                  placeholder="https://…/SKILL.md"
                  className="min-w-0 flex-1 rounded-lg border border-border bg-surface px-2 py-1.5 text-[12px] outline-none focus:border-accent/50"
                />
                <button
                  onClick={installSkill}
                  disabled={installing || !installUrl.trim()}
                  className="shrink-0 rounded-lg bg-accent px-2.5 py-1.5 text-[12px] text-white disabled:opacity-40"
                >
                  {installing ? "…" : "安装"}
                </button>
              </div>
            </div>
          </div>
        )}

        {atOpen && atMatches.length > 0 && (
          <div className="absolute bottom-[calc(100%+8px)] left-0 z-20 w-80 rounded-2xl border border-border bg-surface p-1.5 shadow-lg">
            <div className="px-2 py-1 text-[11px] font-medium uppercase tracking-wide text-faint">引用工作区文件作为上下文</div>
            <div className="max-h-[40vh] overflow-y-auto">
              {atMatches.map((f, i) => (
                <button
                  key={f.name}
                  onMouseEnter={() => setAtSel(i)}
                  onClick={() => pickFile(f.name)}
                  className={"flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left " + (i === atSel ? "bg-surface2" : "hover:bg-surface2")}
                >
                  <span className="text-[15px]">{IMAGE_RE.test(f.name) ? "🖼️" : "📄"}</span>
                  <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{f.name}</span>
                  <span className="shrink-0 text-[11px] text-faint">{(f.size / 1024).toFixed(1)}KB</span>
                </button>
              ))}
            </div>
            <div className="px-2 pt-1 text-[10.5px] text-faint">↑↓ 选择 · Enter 引用 · Esc 取消</div>
          </div>
        )}

        <div className="mb-2 flex flex-wrap items-center gap-1.5">
          {!toolsOpen ? (
            // Collapsed: the default is automatic tool selection — make that
            // obvious instead of showing four greyed chips that look "off".
            <button
              onClick={() => setToolsOpen(true)}
              title="默认按任务自动选择工具，点击可限定工具范围"
              className="inline-flex items-center gap-1 rounded-full border border-border px-2.5 py-1 text-[12px] text-muted hover:bg-surface2"
            >
              🧰 {enabledGroups.size === 0 ? "工具：自动选择" : `工具：仅 ${enabledGroups.size} 类`}
              <span className="text-faint">⌄</span>
            </button>
          ) : (
            <>
              <span className="text-[11px] text-faint">限定范围：</span>
              {TOOL_GROUPS.map((g) => {
                const on = enabledGroups.has(g.id);
                return (
                  <button
                    key={g.id}
                    onClick={() => onToggleGroup(g.id)}
                    title={g.desc}
                    aria-pressed={on}
                    className={
                      "rounded-full border px-2.5 py-1 text-[12px] transition " +
                      (on ? "border-accent/40 bg-accentsoft text-accent" : "border-border text-faint hover:bg-surface2")
                    }
                  >
                    {g.icon} {g.label}
                  </button>
                );
              })}
              {enabledGroups.size > 0 && (
                <button onClick={onClearGroups} className="rounded-full px-2 py-1 text-[12px] text-faint hover:text-accent" title="恢复按任务自动选择全部工具">
                  重置为自动
                </button>
              )}
              <button onClick={() => setToolsOpen(false)} className="ml-0.5 text-[11px] text-faint hover:text-ink">收起</button>
            </>
          )}
          {skill && (
            <span className="ml-auto inline-flex items-center gap-1 rounded-full bg-accentsoft px-2 py-1 text-[12px] text-accent">
              {skill.icon} {skill.label} 模式
              <button onClick={() => onPickSkill(null)} aria-label="退出技能模式" className="hover:text-ink">✕</button>
            </span>
          )}
        </div>

        {(attachments.length > 0 || uploading > 0) && (
          <div className="mb-2 flex flex-wrap gap-1.5">
            {attachments.map((a, i) => (
              <span key={a.path} className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-surface2/60 px-2 py-1 text-[12px] text-ink">
                <span>{a.image ? "🖼️" : "📄"}</span>
                <span className="max-w-[160px] truncate">{a.name}</span>
                <button onClick={() => setAttachments((xs) => xs.filter((_, j) => j !== i))} aria-label={"移除 " + a.name} className="text-faint hover:text-accent">✕</button>
              </span>
            ))}
            {uploading > 0 && <span className="rounded-lg bg-surface2/60 px-2 py-1 text-[12px] text-faint">上传中 {uploading}…</span>}
          </div>
        )}

        <div
          onDragOver={(e) => { e.preventDefault(); if (!dragging) setDragging(true); }}
          onDragLeave={(e) => { if (e.currentTarget === e.target) setDragging(false); }}
          onDrop={onDrop}
          className={
            "relative flex items-end gap-2 rounded-[26px] border bg-surface px-2 py-2 shadow-[0_2px_18px_rgba(40,38,32,0.06)] transition " +
            (dragging ? "border-accent border-dashed bg-accentsoft/30" : "border-border")
          }
        >
          {dragging && (
            <div className="pointer-events-none absolute inset-0 z-10 grid place-items-center rounded-[26px] bg-accentsoft/60 text-[13px] font-medium text-accent backdrop-blur-sm">
              松开即可上传文件 / 图片
            </div>
          )}
          <button
            onClick={() => fileRef.current?.click()}
            className="grid h-10 w-10 shrink-0 place-items-center rounded-full text-muted hover:bg-surface2 transition"
            title="上传文件 / 图片（也可拖拽或粘贴）"
            aria-label="上传文件或图片"
          >
            <span className="text-[17px]">📎</span>
          </button>
          <input
            ref={fileRef}
            type="file"
            multiple
            accept="image/*,.txt,.md,.json,.csv,.log,.py,.js,.ts,.go,.html,.css,.yaml,.yml,.xml"
            hidden
            onChange={(e) => onFiles(e.target.files)}
          />
          <textarea
            ref={taRef}
            value={text}
            onChange={(e) => { setText(e.target.value); syncAt(e.target.value, e.target.selectionStart); }}
            onPaste={onPaste}
            onKeyDown={(e) => {
              if (atOpen && atMatches.length) {
                if (e.key === "ArrowDown") { e.preventDefault(); setAtSel((s) => Math.min(s + 1, atMatches.length - 1)); return; }
                if (e.key === "ArrowUp") { e.preventDefault(); setAtSel((s) => Math.max(s - 1, 0)); return; }
                if (e.key === "Enter" || e.key === "Tab") { e.preventDefault(); pickFile(atMatches[atSel].name); return; }
                if (e.key === "Escape") { e.preventDefault(); setAtOpen(false); return; }
              }
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                send();
              }
              if (e.key === "Escape") setMenu(false);
            }}
            rows={1}
            placeholder="给 Orka 发消息…  / 选技能 · @ 引用文件 · 拖拽或粘贴上传"
            className="block max-h-[200px] flex-1 resize-none bg-transparent px-1 py-2 text-[15px] outline-none placeholder:text-faint"
          />
          {busy ? (
            <button
              onClick={onKill}
              className="grid h-10 w-10 shrink-0 place-items-center rounded-full bg-ink text-bg hover:opacity-80 transition"
              title="Stop"
            >
              <span className="h-3 w-3 rounded-[3px] bg-bg" />
            </button>
          ) : (
            <button
              onClick={send}
              disabled={(!text.trim() && attachments.length === 0) || uploading > 0}
              className="grid h-10 w-10 shrink-0 place-items-center rounded-full bg-accent text-white hover:brightness-105 disabled:opacity-30 transition"
              title="Send"
              aria-label="发送"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
                <path d="M12 19V5M12 5l-6 6M12 5l6 6" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
          )}
        </div>
        <p className="mt-2 text-center text-[11px] text-faint">
          Orka 会自动选择工具(搜索 · 网页 · 天气 · 文件 · 浏览器 · 换算 · 编码)。可能出错,工具操作会作用于你的工作区。
        </p>
      </div>
    </div>
  );
}
