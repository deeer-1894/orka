import type { RunStatus } from "../hooks/useChatStream";

const SIGNAL: Record<RunStatus, { color: string; label: string; pulse?: boolean }> = {
  idle: { color: "var(--color-faint)", label: "IDLE" },
  streaming: { color: "var(--color-live)", label: "STREAMING", pulse: true },
  paused: { color: "var(--color-clarify)", label: "AWAITING USER", pulse: true },
  done: { color: "var(--color-ok)", label: "DONE" },
  error: { color: "var(--color-danger)", label: "FAILED" },
};

export function StatusBar({
  status,
  model,
  runMode,
  trace,
}: {
  status: RunStatus;
  model: string;
  runMode: string;
  trace?: string;
}) {
  const s = SIGNAL[status];
  return (
    <header className="flex items-center justify-between border-b hair px-5 h-14 bg-panel/60 backdrop-blur-sm">
      <div className="flex items-center gap-3">
        <div className="flex items-baseline gap-2">
          <span className="font-display font-extrabold tracking-tight text-[19px] text-text">
            CAVIS
          </span>
          <span className="font-mono text-[10px] text-faint tracking-[0.3em] uppercase">
            control room
          </span>
        </div>
        <span className="ml-2 h-5 w-px bg-line" />
        <span className="font-mono text-[11px] text-muted uppercase tracking-wider">
          {runMode === "graph" ? "graph · deterministic" : "adk · model-driven"}
        </span>
      </div>

      <div className="flex items-center gap-5">
        <span className="font-mono text-[11px] text-faint">
          trace <span className="text-muted">{trace ? trace.slice(0, 12) : "—"}</span>
        </span>
        <span className="font-mono text-[11px] text-faint hidden sm:inline">{model}</span>
        <div className="flex items-center gap-2 rounded-full border hair px-3 py-1">
          <span
            className={"h-2 w-2 rounded-full " + (s.pulse ? "pulse-dot" : "")}
            style={{ background: s.color, boxShadow: `0 0 10px ${s.color}` }}
          />
          <span className="font-mono text-[10px] tracking-[0.2em]" style={{ color: s.color }}>
            {s.label}
          </span>
        </div>
      </div>
    </header>
  );
}
