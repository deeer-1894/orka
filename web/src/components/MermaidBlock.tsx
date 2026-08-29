import { useEffect, useRef, useState } from "react";

// MermaidBlock renders a diagram (flowchart, sequence, ER, etc.) from mermaid
// source — the basis for architecture/topology/data-flow artifacts. securityLevel
// "strict" sanitizes labels and disables click-handlers, so it's safe on a public
// page. On a parse error it falls back to showing the source.
//
// mermaid is imported DYNAMICALLY: statically importing it pulled its ~1MB core
// into the main bundle, so every visitor paid for it on first load even if they
// never opened an artifact containing a diagram. Now it is fetched the first time
// a diagram actually renders.
let initialized = false;
export function MermaidBlock({ src }: { src: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [err, setErr] = useState(false);

  useEffect(() => {
    let alive = true;
    setErr(false);
    const id = "mmd-" + Math.random().toString(36).slice(2);
    (async () => {
      try {
        const mermaid = (await import("mermaid")).default;
        if (!initialized) {
          mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: "neutral", fontFamily: "inherit" });
          initialized = true;
        }
        const { svg } = await mermaid.render(id, src.trim());
        if (alive && ref.current) ref.current.innerHTML = svg;
      } catch {
        if (alive) setErr(true); // unparseable source, or the chunk failed to load
      }
    })();
    return () => { alive = false; };
  }, [src]);

  if (err) {
    return (
      <pre className="overflow-x-auto rounded-xl border border-border bg-surface2/40 p-3 font-mono text-[12px] text-muted">{src}</pre>
    );
  }
  return <div ref={ref} className="overflow-x-auto rounded-xl border border-border bg-surface2/30 p-3 text-center [&_svg]:mx-auto [&_svg]:max-w-full" />;
}
