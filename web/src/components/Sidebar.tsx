import { useEffect, useMemo, useState } from "react";
import type { Conversation, MessageSearchHit } from "../types";
import { api } from "../api";
import { Icon } from "./Icon";
import { confirmDialog } from "../lib/confirm";

// Time buckets so a long history reads as 今天/昨天/近7天/更早 instead of one
// undifferentiated list.
function groupByTime(cs: Conversation[]): { label: string; items: Conversation[] }[] {
  const d = new Date();
  const today = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const day = 86400000;
  const order = ["今天", "昨天", "近 7 天", "更早"];
  const buckets: Record<string, Conversation[]> = { 今天: [], 昨天: [], "近 7 天": [], 更早: [] };
  for (const c of cs) {
    const t = c.created_at || 0;
    if (t >= today) buckets["今天"].push(c);
    else if (t >= today - day) buckets["昨天"].push(c);
    else if (t >= today - 6 * day) buckets["近 7 天"].push(c);
    else buckets["更早"].push(c);
  }
  return order.filter((l) => buckets[l].length).map((label) => ({ label, items: buckets[label] }));
}

// highlight wraps occurrences of q in the snippet with an accent <mark>, so the
// matched term stands out in the search-result preview.
function highlight(text: string, q: string): React.ReactNode {
  if (!q) return text;
  const parts: React.ReactNode[] = [];
  const lower = text.toLowerCase();
  const ql = q.toLowerCase();
  let i = 0;
  let k = 0;
  for (;;) {
    const at = lower.indexOf(ql, i);
    if (at < 0) { parts.push(text.slice(i)); break; }
    if (at > i) parts.push(text.slice(i, at));
    parts.push(<mark key={k++} className="rounded bg-accentsoft text-accent">{text.slice(at, at + q.length)}</mark>);
    i = at + q.length;
  }
  return parts;
}

// relTime is a compact "2小时前 / 3天前 / 6/21" used to disambiguate same-titled
// conversations and on hover.
function relTime(ms?: number): string {
  if (!ms) return "";
  const diff = Date.now() - ms;
  if (diff < 60000) return "刚刚";
  if (diff < 3600000) return Math.floor(diff / 60000) + "分钟前";
  if (diff < 86400000) return Math.floor(diff / 3600000) + "小时前";
  if (diff < 7 * 86400000) return Math.floor(diff / 86400000) + "天前";
  const d = new Date(ms);
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

export function Sidebar({
  open,
  conversations,
  shared,
  activeID,
  runningIds,
  scheduledIds,
  onSelect,
  onSelectClose,
  onNew,
  onRename,
  onDelete,
  onPrune,
  onShare,
  name,
  email,
  onSignOut,
}: {
  open: boolean;
  conversations: Conversation[];
  shared: Conversation[];
  activeID: string;
  runningIds: string[];
  scheduledIds: Set<string>;
  onSelect: (id: string) => void;
  onSelectClose?: () => void;
  onNew: () => void;
  onRename: (id: string, title: string) => void;
  onDelete: (id: string) => void;
  onPrune: () => void;
  onShare: (id: string) => void;
  name: string;
  email: string;
  onSignOut: () => void;
}) {
  const [editing, setEditing] = useState("");
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState("");

  const q = query.trim().toLowerCase();
  const filtered = useMemo(
    () => (q ? conversations.filter((c) => (c.title || "").toLowerCase().includes(q)) : conversations),
    [conversations, q],
  );
  const groups = useMemo(() => groupByTime(filtered), [filtered]);

  // Cross-conversation full-text search: debounce the query and ask the backend
  // for message-body matches (titles are filtered locally above). Hits in
  // conversations whose title already matched are dropped to avoid duplication.
  const [hits, setHits] = useState<MessageSearchHit[]>([]);
  const [searching, setSearching] = useState(false);
  useEffect(() => {
    const term = query.trim();
    if (term.length < 2) { setHits([]); setSearching(false); return; }
    setSearching(true);
    const id = setTimeout(() => {
      api.searchMessages(term).then((r) => setHits(r.hits || [])).catch(() => setHits([])).finally(() => setSearching(false));
    }, 280);
    return () => clearTimeout(id);
  }, [query]);
  const titleIds = useMemo(() => new Set(filtered.map((c) => c.conversation_id)), [filtered]);
  const msgHits = useMemo(() => {
    const seen = new Set<string>();
    return hits.filter((h) => {
      if (titleIds.has(h.conversation_id) || seen.has(h.conversation_id)) return false;
      seen.add(h.conversation_id); // one row per conversation
      return true;
    });
  }, [hits, titleIds]);

  // Titles that appear more than once → show a timestamp to tell them apart.
  const dupTitles = useMemo(() => {
    const seen = new Map<string, number>();
    for (const c of conversations) seen.set(c.title, (seen.get(c.title) || 0) + 1);
    return new Set([...seen].filter(([, n]) => n > 1).map(([t]) => t));
  }, [conversations]);

  const ConvRow = (c: Conversation) => {
    const active = c.conversation_id === activeID;
    const running = runningIds.includes(c.conversation_id);
    const scheduled = scheduledIds.has(c.conversation_id);
    const isBranch = !!c.parent_conversation_id;
    if (editing === c.conversation_id) {
      return (
        <input
          key={c.conversation_id}
          autoFocus
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => {
            if (draft.trim()) onRename(c.conversation_id, draft.trim());
            setEditing("");
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") (e.target as HTMLInputElement).blur();
            if (e.key === "Escape") setEditing("");
          }}
          className="w-full rounded-lg border border-accent/50 bg-surface px-3 py-2 text-[14px] outline-none"
        />
      );
    }
    const dup = dupTitles.has(c.title);
    return (
      <div
        key={c.conversation_id}
        onClick={() => { onSelect(c.conversation_id); onSelectClose?.(); }}
        aria-current={active ? "true" : undefined}
        className={
          "group flex items-center gap-1 rounded-lg px-3 py-2 cursor-pointer transition " +
          (isBranch ? "ml-3 border-l border-border pl-2.5 " : "") +
          (active ? "bg-accentsoft text-ink" : "text-muted hover:bg-surface")
        }
        title={`${isBranch ? "分支 · " : ""}${c.title}${c.created_at ? " · " + relTime(c.created_at) : ""}`}
      >
        {isBranch && <Icon name="share" size={12} className="shrink-0 rotate-90 text-faint" />}
        {running && <span className="inline-flex shrink-0 items-center" role="status"><span className="h-1.5 w-1.5 animate-pulse rounded-full bg-ok" title="运行中" /><span className="sr-only">运行中</span></span>}
        {scheduled && <span className="shrink-0 text-[12px]" title="由定时任务驱动">🔁</span>}
        <span className="flex-1 truncate text-[14px]">{c.title}</span>
        {/* disambiguate same-titled conversations with a timestamp */}
        {dup && <span className="shrink-0 text-[10px] text-faint group-hover:hidden">{relTime(c.created_at)}</span>}
        {(c.shares?.length ?? 0) > 0 && <span className="shrink-0 text-[11px] group-hover:hidden" title={`已分享给 ${c.shares!.length} 人`}>🔗</span>}
        <button onClick={(e) => { e.stopPropagation(); onShare(c.conversation_id); }} className="hidden px-1 text-faint hover:text-accent group-hover:block" title="分享" aria-label="分享会话"><Icon name="share" size={14} /></button>
        <button onClick={(e) => { e.stopPropagation(); setDraft(c.title); setEditing(c.conversation_id); }} className="hidden px-1 text-faint hover:text-ink group-hover:block" title="重命名" aria-label="重命名会话"><Icon name="rename" size={14} /></button>
        <button onClick={async (e) => { e.stopPropagation(); if (await confirmDialog({ title: "删除这个会话?", body: "删除后无法恢复。", confirmText: "删除", danger: true })) onDelete(c.conversation_id); }} className="hidden px-1 text-faint hover:text-accent group-hover:block" title="删除" aria-label="删除会话"><Icon name="trash" size={14} /></button>
      </div>
    );
  };

  return (
    <aside
      className={
        "fixed inset-y-0 left-0 z-40 shrink-0 overflow-hidden border-r border-border bg-surface2 md:static md:z-auto md:bg-surface2/60 flex flex-col transition-all duration-300 " +
        (open ? "w-[264px]" : "w-0")
      }
    >
      <div className="w-[264px] flex flex-col h-full">
        <div className="flex items-center gap-2 px-4 h-14">
          <span className="grid h-7 w-7 place-items-center rounded-lg bg-accent text-white font-serif text-[15px] leading-none">
            O
          </span>
          <span className="font-serif text-[18px] text-ink">Orka</span>
        </div>

        <div className="px-3">
          <button
            onClick={onNew}
            className="w-full flex items-center gap-2 rounded-xl border border-border bg-surface px-3 py-2.5 text-[14px] text-ink hover:bg-surface transition hover:border-accent/40"
          >
            <span className="text-accent text-[16px] leading-none">+</span> New chat
          </button>
        </div>

        <div className="mt-3 px-3">
          <div className="relative">
            <span className="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-faint"><Icon name="search" size={13} /></span>
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="搜索会话…"
              className="w-full rounded-lg border border-border bg-surface px-2.5 py-1.5 pl-7 text-[12.5px] outline-none focus:border-accent/50"
            />
          </div>
        </div>

        <div className="mt-2 flex items-center justify-between px-3">
          <span className="text-[11px] uppercase tracking-wider text-faint">{q ? `${filtered.length} 个结果` : "会话"}</span>
          {conversations.length > 1 && (
            <button onClick={onPrune} className="text-[11px] text-faint hover:text-accent" title="删除所有没有消息的空会话">
              清理空会话
            </button>
          )}
        </div>
        <div className="mt-1 flex-1 overflow-y-auto px-2 pb-2">
          {conversations.length === 0 && <div className="px-2 py-2 text-[13px] text-faint">还没有会话</div>}
          {conversations.length > 0 && filtered.length === 0 && <div className="px-2 py-2 text-[13px] text-faint">无匹配会话</div>}
          {groups.map((g) => (
            <div key={g.label}>
              <div className="px-3 pb-0.5 pt-2 text-[10.5px] uppercase tracking-wider text-faint/80">{g.label}</div>
              {g.items.map(ConvRow)}
            </div>
          ))}

          {/* Cross-conversation message matches (body text), below title matches. */}
          {q && (msgHits.length > 0 || searching) && (
            <div>
              <div className="px-3 pb-0.5 pt-3 text-[10.5px] uppercase tracking-wider text-faint/80">
                消息匹配{searching && msgHits.length === 0 ? " · 搜索中…" : msgHits.length ? ` · ${msgHits.length}` : ""}
              </div>
              {msgHits.map((h) => (
                <div
                  key={h.conversation_id}
                  onClick={() => { onSelect(h.conversation_id); onSelectClose?.(); }}
                  className="group cursor-pointer rounded-lg px-3 py-1.5 text-muted transition hover:bg-surface"
                  title={h.title}
                >
                  <div className="truncate text-[13px] text-ink">{h.title || "未命名会话"}</div>
                  <div className="line-clamp-2 text-[11.5px] text-faint">{highlight(h.snippet, q)}</div>
                </div>
              ))}
            </div>
          )}

          {!q && shared.length > 0 && (
            <>
              <div className="mt-4 mb-1 px-3 text-[11px] uppercase tracking-wider text-faint">分享给我的</div>
              {shared.map((c) => {
                const active = c.conversation_id === activeID;
                const role = c.shares?.find((s) => s.email === email)?.role;
                return (
                  <div
                    key={c.conversation_id}
                    onClick={() => {
                      onSelect(c.conversation_id);
                      onSelectClose?.();
                    }}
                    className={
                      "group flex items-center gap-1 rounded-lg px-3 py-2 cursor-pointer transition " +
                      (active ? "bg-accentsoft text-ink" : "text-muted hover:bg-surface")
                    }
                    title={`由 ${c.owner_email} 分享`}
                  >
                    <span className="shrink-0 text-[11px]">{role === "editor" ? "✏️" : "👁"}</span>
                    <span className="flex-1 truncate text-[14px]" title={c.title}>{c.title}</span>
                    <span className="shrink-0 truncate text-[10.5px] text-faint">{c.owner_email?.split("@")[0]}</span>
                  </div>
                );
              })}
            </>
          )}
        </div>

        <div className="flex items-center gap-2.5 border-t border-border px-3 py-3">
          <div className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-accent/90 text-[12px] text-white">
            {(name || email || "?").slice(0, 1).toUpperCase()}
          </div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px] text-ink">{name || email}</div>
            <div className="truncate text-[11px] text-faint">{email}</div>
          </div>
          <button
            onClick={onSignOut}
            className="rounded-lg px-2 py-1 text-[12px] text-faint hover:bg-surface hover:text-accent"
            title="Sign out"
          >
            ⎋
          </button>
        </div>
      </div>
    </aside>
  );
}
