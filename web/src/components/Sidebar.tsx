import { useEffect, useMemo, useState } from "react";
import type { Conversation, MessageSearchHit, Project } from "../types";
import { api } from "../api";
import { Icon } from "./Icon";
import { Logo } from "./Logo";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { confirmDialog } from "../lib/confirm";
import { COPY } from "../lib/copy";

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

// SectionHeader titles a part of the conversation list; with onToggle it folds
// the part away and shows how many it holds.
function SectionHeader({ label, count, open, onToggle, children }: { label: string; count?: number; open?: boolean; onToggle?: () => void; children?: React.ReactNode }) {
  const text = <>{label}{count !== undefined && <span className="ml-1 font-normal text-faint">· {count}</span>}</>;
  return <div className="mt-3 flex items-center justify-between px-1 first:mt-0">
    {onToggle ? (
      <button onClick={onToggle} aria-expanded={open} className="flex items-center gap-1 rounded-md px-2 py-0.5 text-sm font-medium text-muted hover:text-ink">
        <Icon name="chevron" size={11} className={"transition-transform " + (open ? "" : "-rotate-90")} />{text}
      </button>
    ) : <span className="px-2 text-sm font-medium text-muted">{text}</span>}
    {children}
  </div>;
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
  projects,
  activeProjectID,
  onOpenProject,
  onNewProject,
  onRenameProject,
  onDeleteProject,
  onMove,
  name,
  email,
  onSignOut,
  tasksActive,
  onOpenTasks,
  onOpenSettings,
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
  projects: Project[];
  activeProjectID: string;
  onOpenProject: (id: string) => void;
  onNewProject: () => void;
  onRenameProject: (id: string) => void;
  onDeleteProject: (id: string) => void;
  onMove: (id: string) => void;
  name: string;
  email: string;
  onSignOut: () => void;
  tasksActive: boolean;
  onOpenTasks: () => void;
  onOpenSettings: () => void;
}) {
  const [editing, setEditing] = useState("");
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState("");

  const q = query.trim().toLowerCase();
  const filtered = useMemo(
    () => (q ? conversations.filter((c) => (c.title || "").toLowerCase().includes(q)) : conversations),
    [conversations, q],
  );
  // Task runs live in conversations of their own; they are kept out of the
  // everyday list and folded into one section, opened when one is active.
  const projectIDs = useMemo(() => new Set(projects.map(p => p.project_id)), [projects]);
  const inProject = (c: Conversation) => !!c.project_id && projectIDs.has(c.project_id);
  const own = useMemo(() => filtered.filter((c) => !scheduledIds.has(c.conversation_id) && (!!q || !inProject(c))), [filtered, scheduledIds, projectIDs, q]);
  const activeConvProject = conversations.find(c => c.conversation_id === activeID)?.project_id || "";
  const [openProjects, setOpenProjects] = useState<Set<string>>(new Set());
  useEffect(() => { if (activeConvProject) setOpenProjects(s => s.has(activeConvProject) ? s : new Set(s).add(activeConvProject)); }, [activeConvProject]);
  const taskConvs = useMemo(() => filtered.filter((c) => scheduledIds.has(c.conversation_id)), [filtered, scheduledIds]);
  const groups = useMemo(() => groupByTime(own), [own]);
  const [tasksOpen, setTasksOpen] = useState(false);
  const [sharedOpen, setSharedOpen] = useState(true);
  useEffect(() => { if (scheduledIds.has(activeID)) setTasksOpen(true); }, [activeID, scheduledIds]);

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
          className="w-full rounded-lg border border-accent/50 bg-surface px-3 py-2 text-base outline-none"
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
        <span className="flex-1 truncate text-base">{c.title}</span>
        {/* disambiguate same-titled conversations with a timestamp */}
        {dup && <span className="shrink-0 text-xs text-faint group-hover:hidden">{relTime(c.created_at)}</span>}
        {(c.shares?.length ?? 0) > 0 && <span className="shrink-0 text-faint group-hover:hidden" title={`已分享给 ${c.shares!.length} 人`}><Icon name="link" size={12} /></span>}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button onClick={(e) => e.stopPropagation()} aria-label={`${c.title} 的更多操作`} title="更多操作"
              className="grid h-6 w-6 shrink-0 place-items-center rounded-md text-faint opacity-0 hover:bg-surface2 hover:text-ink focus-visible:opacity-100 group-hover:opacity-100 data-[state=open]:opacity-100">
              <Icon name="more" size={15} />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-40" onClick={(e) => e.stopPropagation()}>
            <DropdownMenuItem onSelect={() => { setDraft(c.title); setEditing(c.conversation_id); }}><Icon name="rename" size={14} className="text-muted" />重命名</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => onShare(c.conversation_id)}><Icon name="share" size={14} className="text-muted" />分享</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => onMove(c.conversation_id)}><Icon name="folder" size={14} className="text-muted" />移到项目…</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={async () => { if (await confirmDialog({ title: "删除这个对话？", body: "删除后无法恢复。", confirmText: "删除", danger: true })) onDelete(c.conversation_id); }} className="text-accent"><Icon name="trash" size={14} />删除</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
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
          <Logo size={32} />
          <span className="font-serif text-xl text-ink">Orka</span>
        </div>

        <div className="px-3">
          <button
            onClick={onNew}
            className="w-full flex items-center gap-2 rounded-xl border border-border bg-surface px-3 py-2.5 text-base text-ink hover:bg-surface transition hover:border-accent/40"
          >
            <Icon name="plus" size={16} className="text-accent" /> {COPY.newChat}
          </button>
          <button
            onClick={() => { onOpenTasks(); onSelectClose?.(); }}
            aria-current={tasksActive ? "page" : undefined}
            className={"mt-1 flex w-full items-center gap-2 rounded-lg px-3 py-2 text-base transition " + (tasksActive ? "bg-accentsoft text-ink" : "text-muted hover:bg-surface")}
          >
            <Icon name="clock" size={15} /> {COPY.tasks}
          </button>
        </div>

        <div className="mt-3 px-3">
          <div className="relative">
            <span className="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-faint"><Icon name="search" size={13} /></span>
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="搜索对话…"
              className="w-full rounded-lg border border-border bg-surface px-2.5 py-1.5 pl-7 text-xs outline-none focus:border-accent/50"
            />
          </div>
        </div>

        <div className="mt-2 flex-1 overflow-y-auto px-2 pb-2">
          {!q && <>
            <SectionHeader label={COPY.project}>
              <button onClick={onNewProject} aria-label="新建项目" title="新建项目" className="grid h-6 w-6 place-items-center rounded-md text-faint hover:bg-surface hover:text-ink"><Icon name="plus" size={14} /></button>
            </SectionHeader>
            {projects.length === 0 && <p className="px-3 pb-1 text-xs leading-relaxed text-faint">把同一课题的对话、文件和要求放在一起。</p>}
            {projects.map(p => {
              const convs = conversations.filter(c => c.project_id === p.project_id);
              const expanded = openProjects.has(p.project_id);
              const toggle = () => setOpenProjects(s => { const n = new Set(s); if (n.has(p.project_id)) n.delete(p.project_id); else n.add(p.project_id); return n; });
              return <div key={p.project_id}>
                <div className={"group flex items-center gap-1 rounded-lg pr-2 transition " + (activeProjectID === p.project_id ? "bg-accentsoft text-ink" : "text-muted hover:bg-surface")}>
                  <button onClick={toggle} aria-expanded={expanded} aria-label={(expanded ? "收起 " : "展开 ") + p.name} className="grid h-8 w-7 shrink-0 place-items-center text-faint hover:text-ink">
                    <Icon name="chevron" size={11} className={"transition-transform " + (expanded ? "" : "-rotate-90")} />
                  </button>
                  <button onClick={() => { onOpenProject(p.project_id); onSelectClose?.(); }} aria-current={activeProjectID === p.project_id ? "page" : undefined} className="flex min-w-0 flex-1 items-center gap-1.5 py-2 text-left text-base">
                    <Icon name="folder" size={14} className="shrink-0" /><span className="truncate">{p.name}</span>
                  </button>
                  {convs.length > 0 && <span className="shrink-0 text-xs text-faint group-hover:hidden">{convs.length}</span>}
                  <DropdownMenu>
                    <DropdownMenuTrigger aria-label={`${p.name} 的项目操作`} title="项目操作"
                      className="grid h-6 w-6 shrink-0 place-items-center rounded-md text-faint opacity-0 outline-none hover:bg-surface2 hover:text-ink focus-visible:opacity-100 group-hover:opacity-100 data-[state=open]:opacity-100">
                      <Icon name="more" size={15} />
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="w-40">
                      <DropdownMenuItem onSelect={() => onRenameProject(p.project_id)}><Icon name="rename" size={14} className="text-muted" />重命名</DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem onSelect={() => onDeleteProject(p.project_id)} className="text-accent"><Icon name="trash" size={14} />删除</DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
                {expanded && <div className="ml-5 border-l border-border pl-1">
                  {convs.length === 0 ? <p className="px-3 py-1.5 text-xs text-faint">暂无对话</p> : convs.map(ConvRow)}
                </div>}
              </div>;
            })}
          </>}
          <SectionHeader label={q ? `${filtered.length} 个结果` : "对话"}>
            {conversations.length > 1 && <DropdownMenu>
              <DropdownMenuTrigger aria-label="对话列表的更多操作" title="更多操作" className="grid h-6 w-6 place-items-center rounded-md text-faint hover:bg-surface hover:text-ink">
                <Icon name="more" size={14} />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-44">
                <DropdownMenuItem onSelect={onPrune}><Icon name="trash" size={14} className="text-muted" />清理空对话</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>}
          </SectionHeader>
          {conversations.length === 0 && (
            <div className="px-2 py-4 text-xs leading-relaxed text-muted">
              还没有对话。点上面的<span className="text-ink">新对话</span>，描述一个目标，Orka 会自己拆步骤并执行。
            </div>
          )}
          {conversations.length > 0 && filtered.length === 0 && <div className="px-2 py-2 text-sm text-faint">无匹配对话</div>}
          {groups.map((g) => (
            <div key={g.label}>
              <div className="px-3 pb-0.5 pt-2 text-xs text-faint">{g.label}</div>
              {g.items.map(ConvRow)}
            </div>
          ))}

          {taskConvs.length > 0 && <>
            <SectionHeader label="任务对话" count={taskConvs.length} open={tasksOpen || !!q} onToggle={() => setTasksOpen((o) => !o)} />
            {(tasksOpen || !!q) && taskConvs.map(ConvRow)}
          </>}

          {/* Cross-conversation message matches (body text), below title matches. */}
          {q && (msgHits.length > 0 || searching) && (
            <div>
              <div className="px-3 pb-0.5 pt-3 text-xs text-faint">
                消息匹配{searching && msgHits.length === 0 ? " · 搜索中…" : msgHits.length ? ` · ${msgHits.length}` : ""}
              </div>
              {msgHits.map((h) => (
                <div
                  key={h.conversation_id}
                  onClick={() => { onSelect(h.conversation_id); onSelectClose?.(); }}
                  className="group cursor-pointer rounded-lg px-3 py-1.5 text-muted transition hover:bg-surface"
                  title={h.title}
                >
                  <div className="truncate text-sm text-ink">{h.title || "未命名对话"}</div>
                  <div className="line-clamp-2 text-xs text-faint">{highlight(h.snippet, q)}</div>
                </div>
              ))}
            </div>
          )}

          {!q && shared.length > 0 && (
            <>
              <SectionHeader label="分享给我的" count={shared.length} open={sharedOpen} onToggle={() => setSharedOpen((o) => !o)} />
              {sharedOpen && shared.map((c) => {
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
                    <span className="shrink-0 text-faint" title={role === "editor" ? "可编辑" : "只读"}><Icon name={role === "editor" ? "rename" : "eye"} size={13} /></span>
                    <span className="flex-1 truncate text-base" title={c.title}>{c.title}</span>
                    <span className="shrink-0 truncate text-2xs text-faint">{c.owner_email?.split("@")[0]}</span>
                  </div>
                );
              })}
            </>
          )}
        </div>

        <div className="flex items-center gap-2.5 border-t border-border px-3 py-3">
          <div className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-accent/90 text-xs text-white">
            {(name || email || "?").slice(0, 1).toUpperCase()}
          </div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm text-ink">{name || email}</div>
            <div className="truncate text-xs text-faint">{email}</div>
          </div>
          <button
            onClick={onOpenSettings}
            className="grid h-8 w-8 place-items-center rounded-lg text-faint hover:bg-surface hover:text-ink"
            title="设置"
            aria-label="设置"
          >
            <Icon name="gear" size={15} />
          </button>
          <button
            onClick={onSignOut}
            className="grid h-8 w-8 place-items-center rounded-lg text-faint hover:bg-surface hover:text-accent"
            title="退出登录"
            aria-label="退出登录"
          >
            <Icon name="logout" size={15} />
          </button>
        </div>
      </div>
    </aside>
  );
}
