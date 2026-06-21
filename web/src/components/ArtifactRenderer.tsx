import type { ArtifactBlock } from "../types";
import { Markdown } from "./Markdown";

// ArtifactRenderer turns an artifact's typed blocks into a page using trusted
// components — no arbitrary HTML/JS, so it's safe to render on a public link.
// Unknown block types degrade to a labelled JSON dump rather than breaking.
export function ArtifactRenderer({ blocks }: { blocks: ArtifactBlock[] }) {
  return (
    <div className="space-y-5">
      {blocks.map((b, i) => (
        <Block key={i} block={b} />
      ))}
    </div>
  );
}

const s = (v: unknown): string => (v == null ? "" : String(v));

function Block({ block }: { block: ArtifactBlock }) {
  const d = block.data || {};
  switch (block.type) {
    case "heading": {
      const level = Math.min(3, Math.max(1, Number(d.level) || 1));
      const cls = level === 1 ? "text-[22px] font-semibold" : level === 2 ? "text-[18px] font-semibold" : "text-[15px] font-medium";
      return <div className={cls + " text-ink"}>{s(d.text)}</div>;
    }
    case "markdown":
      return <div className="md-artifact"><Markdown>{s(d.text)}</Markdown></div>;
    case "metric":
      return <MetricBlock label={s(d.label)} value={s(d.value)} delta={s(d.delta)} />;
    case "badge":
      return <BadgeRow items={Array.isArray(d.items) ? (d.items as Record<string, unknown>[]) : [d]} />;
    case "checklist":
      return <Checklist items={(d.items as ChecklistItem[]) || []} />;
    case "table":
      return <Table columns={(d.columns as string[]) || []} rows={(d.rows as unknown[][]) || []} />;
    case "timeline":
      return <Timeline events={(d.events as TimelineEvent[]) || []} />;
    case "diff":
      return <DiffBlock path={s(d.path)} patch={s(d.patch)} />;
    case "code":
      return <CodeBlock language={s(d.language)} text={s(d.text)} />;
    default:
      return (
        <div className="rounded-lg border border-border bg-surface2/40 p-3 text-[12px] text-muted">
          <span className="text-faint">未知区块 “{block.type}”</span>
          <pre className="mt-1 overflow-x-auto whitespace-pre-wrap">{JSON.stringify(d, null, 2)}</pre>
        </div>
      );
  }
}

function MetricBlock({ label, value, delta }: { label: string; value: string; delta: string }) {
  const up = delta.startsWith("+");
  const down = delta.startsWith("-");
  return (
    <div className="inline-flex min-w-[140px] flex-col rounded-xl border border-border bg-surface2/40 px-4 py-3">
      <span className="text-[11px] uppercase tracking-wide text-faint">{label}</span>
      <span className="mt-1 text-[26px] font-semibold leading-none text-ink">{value}</span>
      {delta && <span className={"mt-1 text-[12px] " + (up ? "text-ok" : down ? "text-accent" : "text-muted")}>{delta}</span>}
    </div>
  );
}

const BADGE_TONE: Record<string, string> = {
  ok: "bg-ok/15 text-ok", warn: "bg-amber-500/15 text-amber-600", danger: "bg-accent/15 text-accent", info: "bg-accentsoft text-accent",
};
function BadgeRow({ items }: { items: Record<string, unknown>[] }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {items.map((it, i) => (
        <span key={i} className={"rounded-full px-2.5 py-1 text-[12px] " + (BADGE_TONE[s(it.tone)] || "bg-surface2 text-muted")}>
          {s(it.label)}
        </span>
      ))}
    </div>
  );
}

type ChecklistItem = { label: string; status?: string };
const STATUS_ICON: Record<string, string> = { done: "✅", doing: "🔵", todo: "⚪️", blocked: "🔴" };
function Checklist({ items }: { items: ChecklistItem[] }) {
  return (
    <div className="flex flex-col gap-1">
      {items.map((it, i) => (
        <div key={i} className="flex items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface2/50">
          <span className="text-[13px]">{STATUS_ICON[s(it.status) || "todo"] || "⚪️"}</span>
          <span className={"text-[13.5px] " + (s(it.status) === "done" ? "text-muted line-through" : "text-ink")}>{it.label}</span>
        </div>
      ))}
    </div>
  );
}

function Table({ columns, rows }: { columns: string[]; rows: unknown[][] }) {
  return (
    <div className="overflow-x-auto rounded-xl border border-border">
      <table className="w-full text-[13px]">
        <thead>
          <tr className="border-b border-border bg-surface2/50">
            {columns.map((c, i) => <th key={i} className="px-3 py-2 text-left font-medium text-ink">{c}</th>)}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i} className="border-b border-border/60 last:border-0">
              {r.map((cell, j) => <td key={j} className="px-3 py-1.5 text-muted">{s(cell)}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

type TimelineEvent = { time?: string; title?: string; detail?: string };
function Timeline({ events }: { events: TimelineEvent[] }) {
  return (
    <ol className="relative ml-3 border-l border-border">
      {events.map((e, i) => (
        <li key={i} className="mb-4 ml-4">
          <span className="absolute -left-[5px] mt-1 h-2.5 w-2.5 rounded-full border border-surface bg-accent" />
          {e.time && <div className="text-[11px] text-faint">{e.time}</div>}
          <div className="text-[14px] font-medium text-ink">{s(e.title)}</div>
          {e.detail && <div className="text-[12.5px] text-muted">{e.detail}</div>}
        </li>
      ))}
    </ol>
  );
}

function DiffBlock({ path, patch }: { path: string; patch: string }) {
  // If a unified-diff-ish patch is given, colour +/- lines; else treat as plain.
  const lines = patch.split("\n");
  return (
    <div className="overflow-hidden rounded-xl border border-border">
      {path && <div className="border-b border-border bg-surface2/50 px-3 py-1.5 font-mono text-[12px] text-ink">{path}</div>}
      <pre className="overflow-x-auto bg-[#161513] py-1 font-mono text-[12px] leading-relaxed">
        {lines.map((ln, i) => {
          const tone = ln.startsWith("+") && !ln.startsWith("+++") ? "text-[#7fd18b] bg-ok/10"
            : ln.startsWith("-") && !ln.startsWith("---") ? "text-[#e8927c] bg-accent/10"
            : ln.startsWith("@@") ? "text-[#9aa0e6]" : "text-[#a8a399]";
          return <div key={i} className={"px-3 " + tone}>{ln || " "}</div>;
        })}
      </pre>
    </div>
  );
}

function CodeBlock({ language, text }: { language: string; text: string }) {
  // Render through Markdown's fenced-code path for consistent styling.
  return <Markdown>{"```" + (language || "") + "\n" + text + "\n```"}</Markdown>;
}
