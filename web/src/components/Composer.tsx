import { useEffect, useRef, useState } from "react";
import type { RunStatus } from "../hooks/useChatStream";
import { TOOL_GROUPS } from "../lib/toolGroups";
import { files as fileApi } from "../api";
import { toastError } from "../lib/toast";

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
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [uploading, setUploading] = useState(0);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const busy = status === "streaming";

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

  // "/" at the start of an empty box opens the skill menu.
  useEffect(() => {
    if (text === "/") setMenu(true);
  }, [text]);

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
  const skill = activeSkill ? SKILLS.find((s) => s.name === activeSkill) : null;

  return (
    <div className="px-5 pb-5">
      <div className="relative mx-auto max-w-3xl">
        {menu && (
          <div className="absolute bottom-[calc(100%+8px)] left-0 z-20 w-72 rounded-2xl border border-border bg-surface p-1.5 shadow-lg">
            <div className="px-2 py-1 text-[11px] font-medium uppercase tracking-wide text-faint">
              技能 · 让 Orka 切换专长
            </div>
            {SKILLS.map((s) => (
              <button
                key={s.name}
                onClick={() => pickSkill(s.name)}
                className="flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left hover:bg-surface2"
              >
                <span className="text-[18px]">{s.icon}</span>
                <span className="min-w-0">
                  <span className="block text-[13px] text-ink">
                    {s.label} <span className="text-faint">/{s.name}</span>
                  </span>
                  <span className="block truncate text-[12px] text-muted">{s.desc}</span>
                </span>
              </button>
            ))}
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

        <div className="flex items-end gap-2 rounded-[26px] border border-border bg-surface px-2 py-2 shadow-[0_2px_18px_rgba(40,38,32,0.06)] transition">
          <button
            onClick={() => fileRef.current?.click()}
            className="grid h-10 w-10 shrink-0 place-items-center rounded-full text-muted hover:bg-surface2 transition"
            title="上传文件 / 图片"
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
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                send();
              }
              if (e.key === "Escape") setMenu(false);
            }}
            rows={1}
            placeholder="给 Orka 发消息…  输入 / 选择技能"
            className="block max-h-48 flex-1 resize-none bg-transparent px-1 py-2 text-[15px] outline-none placeholder:text-faint"
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
