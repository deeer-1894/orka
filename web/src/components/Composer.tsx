import type { SessionRecoveryStore } from '../lib/sessionRecovery';
import { useEffect, useRef, useState } from "react";
import type { RunStatus } from "../hooks/useChatStream";
import { api, files as fileApi } from "../api";
import { invalidateSessionFiles, useFileRevision } from "../hooks/useFileRevision";
import { useConversationDraft } from "../hooks/useConversationDraft";
import { toastError } from "../lib/toast";
import { Icon, fileIconName, type IconName } from "./Icon";
import { RunSettingsChips, RunSettingsMenu, type RunSettingsValue } from "./RunSettings";

// Icons/labels for the built-in skills; installed/custom skills get a default.
const SKILL_ICON: Record<string, { icon: IconName; label: string }> = {
  researcher: { icon: "search", label: "调研" },
  writer: { icon: "rename", label: "写作" },
  coder: { icon: "code", label: "编程" },
  analyst: { icon: "chart", label: "分析" },
  translator: { icon: "globe", label: "翻译" },
};

const IMAGE_RE = /\.(png|jpe?g|gif|webp|bmp)$/i;

// Mirrors the built-in skills registered in the control layer (skills_registry.go).
// Selecting one prepends a directive the model honours via apply_skill.
export const SKILLS = [
  { name: "researcher", label: "调研", icon: "search", desc: "多来源交叉验证 + 引用" },
  { name: "writer", label: "写作", icon: "rename", desc: "结构化专业文案" },
  { name: "coder", label: "编程", icon: "code", desc: "可运行代码 + 设计权衡" },
  { name: "analyst", label: "分析", icon: "chart", desc: "结构化拆解 + 建议" },
  { name: "translator", label: "翻译", icon: "globe", desc: "自然地道的翻译" },
];

interface ComposerProps {
  sessionRecovery?: SessionRecoveryStore;
  draftState?: ReturnType<typeof useConversationDraft>;
  status: RunStatus;
  blocked?: boolean;
  onSend: (msg: string, fileIDs?: string[], conversationID?: string) => void | Promise<unknown>;
  onKill: () => void;
  activeSkill: string | null;
  onPickSkill: (name: string | null) => void;
  runSettings: RunSettingsValue;
  onManageSkills?: () => void;
  conversationID: string;
  ensureConversation: () => Promise<string>;
}

export function Composer(props: ComposerProps) {
  return props.draftState ? <ComposerForm {...props} draft={props.draftState} /> : <StandaloneComposer {...props} />;
}
function StandaloneComposer(props: ComposerProps) {
  const draft = useConversationDraft(props.conversationID, props.sessionRecovery);
  return <ComposerForm {...props} draft={draft} />;
}
function ComposerForm({
  draft, blocked = false, status, onSend, onKill,
  activeSkill, onPickSkill, runSettings, onManageSkills, conversationID, ensureConversation,
}: ComposerProps & { draft: ReturnType<typeof useConversationDraft> }) {
  const { text, setText, attachments, setAttachments } = draft;
  const [sending, setSending] = useState(false);
  const sendingRef = useRef(false);
  const [sendError, setSendError] = useState<{ cid: string; message: string } | null>(null);
  const [menu, setMenu] = useState(false);
  const [skills, setSkills] = useState<{ name: string; description: string; enabled?: boolean }[]>([]);
  const [previewName, setPreviewName] = useState<string | null>(null);
  const [previewCache, setPreviewCache] = useState<Record<string, string>>({});
  // @-mention: reference a workspace file as context (Cursor-style).
  const [atOpen, setAtOpen] = useState(false);
  const [atDir, setAtDir] = useState(".");
  const [atError, setAtError] = useState("");
  const fileRevision = useFileRevision(conversationID);
  const [atQuery, setAtQuery] = useState("");
  const [atSel, setAtSel] = useState(0);
  const [atListing, setAtListing] = useState<{ conversationID: string; items: { name: string; dir: boolean; size: number }[] }>({ conversationID: "", items: [] });
  const atFiles = atListing.conversationID === conversationID ? atListing.items : [];
  const uploadScope = useRef({ conversationID, generation: 0, creating: null as Promise<string> | null, createdID: "" });
  if (uploadScope.current.conversationID !== conversationID) {
    const scope = uploadScope.current;
    // Creating the initial conversation belongs to this upload batch. Other
    // navigation invalidates its attachment callbacks immediately.
    if (!(scope.conversationID === "" && conversationID === scope.createdID)) {
      scope.generation++; scope.creating = null; scope.createdID = "";
    }
    scope.conversationID = conversationID;
  }

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

  // Switched-off skills are managed in settings, not offered here.
  const loadSkills = () => api.listSkills().then((r) => setSkills((r.skills || []).filter(s => s.enabled !== false))).catch(() => {});
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

  useEffect(() => {
    setAtListing({ conversationID, items: [] }); setAtSel(0); setAtDir(".");
  }, [conversationID]);
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
    const batch = Array.from(list);
    const generation = uploadScope.current.generation;
    setUploading(n => n + batch.length);
    try {
      let cid = conversationID;
      if (!cid) {
        const scope = uploadScope.current;
        if (!scope.creating) scope.creating = ensureConversation().then(id => {
          if (scope.generation === generation) { scope.createdID = id; draft.move("", id); }
          return id;
        }).finally(() => { if (scope.generation === generation) scope.creating = null; });
        cid = await scope.creating;
      }
      for (const f of batch) {
        if (uploadScope.current.generation !== generation) break;
        try {
          const path = await fileApi.upload(f, "", undefined, cid);
          invalidateSessionFiles(cid);
          if (uploadScope.current.generation === generation) draft.addAttachment({ name: f.name, path, image: IMAGE_RE.test(f.name), conversationID: cid });
        } catch { toastError("上传失败：" + f.name); }
      }
    } catch { toastError("无法创建上传文件的对话"); }
    finally {
      setUploading(n => n - batch.length);
      if (fileRef.current) fileRef.current.value = "";
    }
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

  // Reload on each picker opening and conversation change; ignore late results.
  useEffect(() => {
    let current = true;
    if (!atOpen || !conversationID) return;
    setAtError("");
    fileApi.scopedList(atDir, conversationID, true)
      .then(items => { if (current) setAtListing({ conversationID, items }); })
      .catch(() => { if (current) setAtListing({ conversationID, items: [] }); setAtError("文件读取失败，请重新打开引用列表"); });
    return () => { current = false; };
  }, [atOpen, conversationID, atDir, fileRevision]);

  // Detect an `@token` being typed at the cursor → open the file picker.
  const syncAt = (value: string, cursor: number | null) => {
    const before = value.slice(0, cursor ?? value.length);
    const m = before.match(/(^|\s)@([^\s@]*)$/);
    if (m) { setAtOpen(true); setAtQuery(m[2]); setAtSel(0); } else setAtOpen(false);
  };
  const atMatches = atFiles
    .filter((f) => f.name.toLowerCase().includes(atQuery.toLowerCase()))
    .slice(0, 8);

  const chooseFile = (file: { name: string; dir: boolean }) => {
    const path = atDir === '.' ? file.name : `${atDir}/${file.name}`;
    if (file.dir) { setAtListing({ conversationID, items: [] }); setAtDir(path); setAtQuery(''); setAtSel(0); }
    else pickFile(path);
  };
  // Insert the picked file as a context attachment and strip the @token.
  const pickFile = (name: string) => {
    const ta = taRef.current;
    const cursor = ta?.selectionStart ?? text.length;
    const before = text.slice(0, cursor).replace(/(^|\s)@([^\s@]*)$/, (_full, pre) => pre);
    setText(before + text.slice(cursor));
    setAtOpen(false);
    setAttachments((a) => (a.some((x) => x.path === name && x.conversationID === conversationID) ? a : [...a, { name, path: name, image: IMAGE_RE.test(name), conversationID }]));
    requestAnimationFrame(() => ta?.focus());
  };

  const send = async () => {
    if (blocked || sendingRef.current || uploading > 0 || (!text.trim() && !attachments.length)) return;
    let cid = conversationID;
    const sent = { text, attachments };
    sendingRef.current = true; setSending(true); setSendError(null);
    try {
      if (!cid) { cid = await ensureConversation(); draft.move("", cid); }
      await onSend(text.trim(), attachments.map(a => a.path), cid);
      draft.clearAccepted(cid, sent); setMenu(false);
    } catch (error) {
      setSendError({ cid, message: "发送失败：" + (error instanceof Error ? error.message : "请重试") });
    } finally { sendingRef.current = false; setSending(false); }
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
  const skill: { icon: IconName; label: string } | null = activeSkill ? { icon: SKILL_ICON[activeSkill]?.icon || "sparkle", label: SKILL_ICON[activeSkill]?.label || activeSkill } : null;

  return (
    <div className="px-5 pb-5">
      <div className="relative mx-auto max-w-3xl">
        {menu && (
          <div ref={menuRef} className="absolute bottom-[calc(100%+8px)] left-0 z-20 w-80 rounded-2xl border border-border bg-surface p-1.5 shadow-lg">
            <div className="px-2 py-1 text-2xs font-medium uppercase tracking-wide text-faint">
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
                        <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-surface2 text-muted"><Icon name={meta?.icon || "sparkle"} size={18} /></span>
                        <span className="min-w-0">
                          <span className="block text-sm text-ink">
                            {meta?.label || s.name} <span className="text-faint">/{s.name}</span>
                            {!builtin && <span className="ml-1 rounded bg-surface2 px-1 text-2xs text-faint">已安装</span>}
                          </span>
                          <span className="block truncate text-xs text-muted">{s.description}</span>
                        </span>
                      </button>
                      <button
                        onClick={() => togglePreview(s.name)}
                        title="查看技能内容"
                        aria-label={"查看技能 " + s.name + " 的内容"}
                        aria-expanded={previewName === s.name}
                        className={
                          "shrink-0 rounded-md px-1.5 py-1 text-sm transition group-hover:opacity-100 " +
                          (previewName === s.name ? "text-accent opacity-100" : "text-faint opacity-0 hover:bg-surface hover:text-ink")
                        }
                      >
                        {previewName === s.name ? <Icon name="chevron" size={14} className="rotate-180" /> : <Icon name="eye" size={14} />}
                      </button>
                    </div>
                    {previewName === s.name && (
                      <div className="mx-2 mb-1 max-h-40 overflow-y-auto whitespace-pre-wrap rounded-lg border border-border bg-surface2/50 px-2.5 py-2 text-xs leading-relaxed text-muted">
                        {previewCache[s.name] ?? "加载中…"}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
            {onManageSkills && <button onClick={() => { setMenu(false); onManageSkills(); }} className="mt-1 flex w-full items-center gap-1.5 border-t border-border px-2 pb-1 pt-2 text-left text-xs text-muted hover:text-ink">
              <Icon name="gear" size={13} />管理技能…
            </button>}
          </div>
        )}

        {atOpen && (
          <div className="absolute bottom-[calc(100%+8px)] left-0 z-20 w-80 rounded-2xl border border-border bg-surface p-1.5 shadow-lg">
            <div className="px-2 py-1 text-2xs font-medium uppercase tracking-wide text-faint">引用工作区文件作为上下文 · {atDir}</div>
            {atDir !== "." && <button onClick={() => { setAtDir(atDir.includes("/") ? atDir.slice(0, atDir.lastIndexOf("/")) : "."); setAtListing({ conversationID, items: [] }); }}>返回上级目录</button>}
            {atError && <p role="alert">{atError}</p>}
            <div className="max-h-[40vh] overflow-y-auto">
              {atMatches.map((f, i) => (
                <button
                  key={f.name}
                  onMouseEnter={() => setAtSel(i)}
                  onClick={() => chooseFile(f)}
                  aria-label={f.dir ? "打开目录 " + f.name : "引用 " + (atDir === "." ? f.name : atDir + "/" + f.name)}
                  className={"flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left " + (i === atSel ? "bg-surface2" : "hover:bg-surface2")}
                >
                  <Icon name={fileIconName(f.name)} size={15} className="shrink-0 text-muted" />
                  <span className="min-w-0 flex-1 truncate text-sm text-ink">{f.name}</span>
                  <span className="shrink-0 text-2xs text-faint">{(f.size / 1024).toFixed(1)}KB</span>
                </button>
              ))}
            </div>
            <div className="px-2 pt-1 text-2xs text-faint">↑↓ 选择 · Enter 引用 · Esc 取消</div>
          </div>
        )}

        <div className="mb-2 flex flex-wrap items-center gap-1.5">
          <RunSettingsChips value={runSettings} />
          {skill && (
            <span className="ml-auto inline-flex items-center gap-1 rounded-full bg-accentsoft px-2 py-1 text-xs text-accent">
              <Icon name={skill.icon} size={13} /> {skill.label} 模式
              <button onClick={() => onPickSkill(null)} aria-label="退出技能模式" className="hover:text-ink"><Icon name="close" size={12} /></button>
            </span>
          )}
        </div>

        {sendError?.cid === conversationID && <p role="alert" className="mb-2 text-sm text-accent">{sendError.message}</p>}
        {(attachments.length > 0 || uploading > 0) && (
          <div className="mb-2 flex flex-wrap gap-1.5">
            {attachments.map((a) => (
              <span key={a.path} className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-surface2/60 px-2 py-1 text-xs text-ink">
                <Icon name={a.image ? "image" : "file"} size={13} className="shrink-0" />
                <span className="max-w-[160px] truncate">{a.name}</span>
                <button onClick={() => setAttachments(xs => xs.filter(item => item !== a))} aria-label={"移除 " + a.name} className="text-faint hover:text-accent"><Icon name="close" size={12} /></button>
              </span>
            ))}
            {uploading > 0 && <span className="rounded-lg bg-surface2/60 px-2 py-1 text-xs text-faint">上传中 {uploading}…</span>}
          </div>
        )}

        <div
          onDragOver={(e) => { e.preventDefault(); if (!dragging) setDragging(true); }}
          onDragLeave={(e) => { if (e.currentTarget === e.target) setDragging(false); }}
          onDrop={onDrop}
          className={
            "composer-input-shell relative flex items-end gap-2 rounded-[26px] border bg-surface px-2 py-2 shadow-[0_2px_18px_rgba(40,38,32,0.06)] transition " +
            (dragging ? "border-accent border-dashed bg-accentsoft/30" : "border-border")
          }
        >
          {dragging && (
            <div className="pointer-events-none absolute inset-0 z-10 grid place-items-center rounded-[26px] bg-accentsoft/60 text-sm font-medium text-accent backdrop-blur-sm">
              松开即可上传文件 / 图片
            </div>
          )}
          <div className="composer-toolbar" role="group" aria-label="消息附件与能力">
          <button
            onClick={() => fileRef.current?.click()}
            className="composer-icon"
            title="上传文件 / 图片（也可拖拽或粘贴）"
            aria-label="上传文件或图片"
          >
            <Icon name="paperclip" size={18} />
          </button>
          <input
            ref={fileRef}
            type="file"
            multiple
            accept="image/*,.txt,.md,.json,.csv,.log,.py,.js,.ts,.go,.html,.css,.yaml,.yml,.xml"
            hidden
            onChange={(e) => onFiles(e.target.files)}
          />
          {/* Surface the two hidden affordances as real buttons (discoverability) */}
          <button
            onClick={() => { setMenu(v => !v); taRef.current?.focus(); }}
            aria-expanded={menu}
            className="composer-icon"
            title="选技能(也可输入 /)"
            aria-label="选技能"
          >
            <Icon name="sparkle" size={17} />
          </button>
          <button
            onClick={() => {
              const v = text + (text && !/\s$/.test(text) ? " @" : "@");
              setText(v);
              taRef.current?.focus();
              syncAt(v, v.length);
            }}
            className="composer-icon"
            title="引用工作区文件(也可输入 @)"
            aria-label="引用文件"
          >
            <Icon name="at" size={17} />
          </button>
          <RunSettingsMenu value={runSettings} />
          </div>
          <textarea
            id="chat-input"
            ref={taRef}
            value={text}
            onChange={(e) => { setText(e.target.value); syncAt(e.target.value, e.target.selectionStart); }}
            onPaste={onPaste}
            onKeyDown={(e) => {
              if (e.nativeEvent.isComposing) return;
              if (atOpen && atMatches.length) {
                if (e.key === "ArrowDown") { e.preventDefault(); setAtSel((s) => Math.min(s + 1, atMatches.length - 1)); return; }
                if (e.key === "ArrowUp") { e.preventDefault(); setAtSel((s) => Math.max(s - 1, 0)); return; }
                if (e.key === "Enter" || e.key === "Tab") { e.preventDefault(); chooseFile(atMatches[atSel]); return; }
                if (e.key === "Escape") { e.preventDefault(); setAtOpen(false); return; }
              }
              // Enter sends; Shift+Enter is a newline. ⌘/Ctrl+Enter also sends —
              // it is the reflex from editors and chat apps, and it works even
              // when a menu has swallowed the plain Enter.
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                send();
              } else if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                send();
              }
              if (e.key === "Escape") setMenu(false);
            }}
            rows={1}
            placeholder={busy ? "运行中 · 发消息可以立即插入当前任务" : "给 Orka 发消息…"}
            className="block min-w-0 max-h-[200px] flex-1 resize-none bg-transparent px-1 py-2 text-md outline-none placeholder:text-faint"
          />
          {busy && !text.trim() && attachments.length === 0 ? (
            <button
              onClick={onKill}
              className="composer-primary-action grid h-10 w-10 shrink-0 place-items-center rounded-full bg-ink text-bg hover:opacity-80 transition"
              title="停止"
              aria-label="停止"
            >
              <span className="h-3 w-3 rounded-[3px] bg-bg" />
            </button>
          ) : (
            <button
              onClick={send}
              disabled={sending || blocked || (!text.trim() && attachments.length === 0) || uploading > 0}
              className="composer-primary-action grid h-10 w-10 shrink-0 place-items-center rounded-full bg-accent text-white hover:brightness-105 disabled:opacity-30 transition"
              title="发送"
              aria-label="发送"
            >
              <Icon name="arrowUp" size={16} strokeWidth={2.2} />
            </button>
          )}
        </div>

      </div>
    </div>
  );
}
