import { useEffect, useRef } from "react";
import { auth } from "../api";

// useEventStream subscribes to the per-user SSE event bus (/events) and invokes
// onEvent(kind) for each pushed signal ("notification" | "run" | …). It augments
// (does not replace) the resource pollers: background work — a scheduled run
// finishing, an unattended failure — now refreshes the relevant UI immediately
// instead of waiting for the next poll tick. Reconnects with backoff; polling
// remains the fallback if the stream can't be established.
export function useEventStream(onEvent: (kind: string) => void) {
  const cbRef = useRef(onEvent);
  cbRef.current = onEvent;

  useEffect(() => {
    const ctrl = new AbortController();
    let stopped = false;
    let attempt = 0;

    const run = async () => {
      while (!stopped) {
        try {
          const res = await fetch("/api/v1/controller/events", {
            headers: { ...(auth.token() ? { Authorization: "Bearer " + auth.token() } : {}) },
            signal: ctrl.signal,
          });
          if (!res.body || res.status !== 200) throw new Error("no stream");
          attempt = 0; // connected
          const reader = res.body.getReader();
          const dec = new TextDecoder();
          let buf = "";
          for (;;) {
            const { done, value } = await reader.read();
            if (done) break;
            buf += dec.decode(value, { stream: true });
            let i: number;
            while ((i = buf.indexOf("\n\n")) >= 0) {
              const frame = buf.slice(0, i);
              buf = buf.slice(i + 2);
              for (const ln of frame.split("\n")) {
                if (!ln.startsWith("data:")) continue;
                try {
                  const { kind } = JSON.parse(ln.slice(5).trim());
                  if (kind && kind !== "hello") cbRef.current(kind);
                } catch { /* skip malformed */ }
              }
            }
          }
        } catch {
          if (stopped || ctrl.signal.aborted) return;
        }
        // reconnect with capped backoff (1s → 15s)
        attempt++;
        await new Promise((r) => setTimeout(r, Math.min(15000, 1000 * attempt)));
      }
    };
    run();
    return () => { stopped = true; ctrl.abort(); };
  }, []);
}
