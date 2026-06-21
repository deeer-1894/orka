import type { ArtifactBlock } from "../types";
import { Markdown } from "./Markdown";
import { MermaidBlock } from "./MermaidBlock";

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
    case "chart":
      return <ChartBlock kind={s(d.kind) || "bar"} title={s(d.title)} data={Array.isArray(d.data) ? (d.data as ChartPoint[]) : []} />;
    case "mermaid":
      return <MermaidBlock src={s(d.src)} />;
    case "html":
      return <HtmlBlock html={s(d.src || d.html)} height={Number(d.height) || 300} />;
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

// ── chart: lightweight themeable SVG (no chart dependency) ────────────────────
type ChartPoint = { label?: string; value?: number };
const CHART_COLORS = ["#e8927c", "#7fb88a", "#7c9fe8", "#e8c97c", "#b88ad9", "#7cd0e8", "#e87cae"];
function ChartBlock({ kind, title, data }: { kind: string; title: string; data: ChartPoint[] }) {
  const pts = data.map((p) => ({ label: s(p.label), value: Number(p.value) || 0 }));
  return (
    <div className="rounded-xl border border-border bg-surface2/30 p-3">
      {title && <div className="mb-2 text-[12.5px] font-medium text-ink">{title}</div>}
      {pts.length === 0 ? (
        <div className="py-6 text-center text-[12px] text-faint">(无数据)</div>
      ) : kind === "pie" ? (
        <PieChart pts={pts} />
      ) : kind === "line" ? (
        <LineChart pts={pts} />
      ) : (
        <BarChart pts={pts} />
      )}
    </div>
  );
}

function BarChart({ pts }: { pts: { label: string; value: number }[] }) {
  const max = Math.max(1, ...pts.map((p) => p.value));
  const W = 520, H = 180, pad = 28, bw = (W - pad * 2) / pts.length;
  return (
    <svg viewBox={`0 0 ${W} ${H + 24}`} className="w-full">
      {pts.map((p, i) => {
        const h = ((H - pad) * p.value) / max;
        const x = pad + i * bw + bw * 0.15;
        return (
          <g key={i}>
            <rect x={x} y={H - h} width={bw * 0.7} height={h} rx="3" fill="var(--color-accent)" opacity="0.85" />
            <text x={x + bw * 0.35} y={H - h - 4} textAnchor="middle" className="fill-[var(--color-muted)]" fontSize="9">{p.value}</text>
            <text x={x + bw * 0.35} y={H + 14} textAnchor="middle" className="fill-[var(--color-faint)]" fontSize="9">{trunc(p.label, 8)}</text>
          </g>
        );
      })}
    </svg>
  );
}

function LineChart({ pts }: { pts: { label: string; value: number }[] }) {
  const max = Math.max(1, ...pts.map((p) => p.value));
  const W = 520, H = 180, pad = 28;
  const xs = (i: number) => pad + (i * (W - pad * 2)) / Math.max(1, pts.length - 1);
  const ys = (v: number) => H - pad - ((H - pad * 1.5) * v) / max;
  const path = pts.map((p, i) => `${i ? "L" : "M"}${xs(i)},${ys(p.value)}`).join(" ");
  return (
    <svg viewBox={`0 0 ${W} ${H + 24}`} className="w-full">
      <path d={path} fill="none" stroke="var(--color-accent)" strokeWidth="2" />
      {pts.map((p, i) => (
        <g key={i}>
          <circle cx={xs(i)} cy={ys(p.value)} r="3" fill="var(--color-accent)" />
          <text x={xs(i)} y={H + 14} textAnchor="middle" className="fill-[var(--color-faint)]" fontSize="9">{trunc(p.label, 8)}</text>
        </g>
      ))}
    </svg>
  );
}

function PieChart({ pts }: { pts: { label: string; value: number }[] }) {
  const total = pts.reduce((a, p) => a + p.value, 0) || 1;
  let acc = 0;
  const R = 70, C = 90;
  return (
    <div className="flex flex-wrap items-center gap-4">
      <svg viewBox="0 0 180 180" className="h-[150px] w-[150px]">
        {pts.map((p, i) => {
          const a0 = (acc / total) * 2 * Math.PI;
          acc += p.value;
          const a1 = (acc / total) * 2 * Math.PI;
          const large = a1 - a0 > Math.PI ? 1 : 0;
          const x0 = C + R * Math.sin(a0), y0 = C - R * Math.cos(a0);
          const x1 = C + R * Math.sin(a1), y1 = C - R * Math.cos(a1);
          return <path key={i} d={`M${C},${C} L${x0},${y0} A${R},${R} 0 ${large} 1 ${x1},${y1} Z`} fill={CHART_COLORS[i % CHART_COLORS.length]} />;
        })}
      </svg>
      <div className="flex flex-col gap-1 text-[12px]">
        {pts.map((p, i) => (
          <div key={i} className="flex items-center gap-1.5">
            <span className="h-2.5 w-2.5 rounded-sm" style={{ background: CHART_COLORS[i % CHART_COLORS.length] }} />
            <span className="text-muted">{p.label}</span>
            <span className="text-faint">{Math.round((p.value / total) * 100)}%</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function trunc(s2: string, n: number) { return s2.length > n ? s2.slice(0, n) + "…" : s2; }

// ── html: free-form escape hatch, isolated in a sandboxed iframe ──────────────
// sandbox=allow-scripts (NOT allow-same-origin) blocks access to the parent
// origin/cookies; an injected CSP blocks network egress so a public page can't
// exfiltrate. For static mockups/visualizations, not data access.
function HtmlBlock({ html, height }: { html: string; height: number }) {
  const csp = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; script-src 'unsafe-inline'; connect-src 'none'">`;
  const doc = `<!doctype html><html><head><meta charset="utf-8">${csp}<style>body{margin:0;font-family:system-ui,sans-serif;color:#222}</style></head><body>${html}</body></html>`;
  return (
    <iframe
      title="html-block"
      sandbox="allow-scripts"
      srcDoc={doc}
      className="w-full rounded-xl border border-border bg-white"
      style={{ height: Math.min(800, Math.max(80, height)) }}
    />
  );
}
