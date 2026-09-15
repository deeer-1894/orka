import { useMemo } from "react";
import type { RunRecord } from "../types";
import { lineDiff, diffStats } from "../lib/diff";
import { fmtRunWhen as fmtWhen } from "../lib/runPresentation";
// RunDiff shows what changed between a scheduled run and the previous run of the
// same task — the core "what's different since last time" view for unattended
// automation. Diffs the answer text (falling back to the error when a run had
// no output), reusing the line-diff used by the file-version history.
export function RunDiff({ prev, cur }: { prev: RunRecord; cur: RunRecord }) {
  const prevText = prev.output || prev.error || "";
  const curText = cur.output || cur.error || "";
  const rows = useMemo(() => lineDiff(prevText, curText), [prevText, curText]);
  const stats = diffStats(rows);
  return (
    <div className="mt-2 overflow-hidden rounded-lg border border-border bg-surface">
      <div className="flex items-center gap-2 border-b border-border px-2.5 py-1.5 text-[11px]">
        <span className="text-muted">{fmtWhen(prev.created_at)} → 本次</span>
        <span className="text-ok">+{stats.add}</span>
        <span className="text-accent">−{stats.del}</span>
      </div>
      <div className="max-h-56 overflow-auto px-1 py-1 font-mono text-[11.5px]">
        {prevText === curText ? (
          <div className="px-2 py-3 text-center text-[12px] text-muted">两次运行结果一致,没有变化。</div>
        ) : (
          rows.map((r, i) => (
            <div
              key={i}
              className={
                "whitespace-pre-wrap break-words px-2 " +
                (r.type === "add" ? "bg-ok/10 text-ok" : r.type === "del" ? "bg-accent/10 text-accent" : "text-muted")
              }
            >
              <span className="mr-2 select-none text-faint">{r.type === "add" ? "+" : r.type === "del" ? "−" : " "}</span>
              {r.text || " "}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
