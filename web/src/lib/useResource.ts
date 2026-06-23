import { useEffect, useState } from "react";

// A tiny shared-cache + polling layer. Multiple components asking for the same
// `key` share ONE fetch + ONE interval (no duplicate requests, no refetch-flash
// on tab switch), and all polling pauses while the page is hidden. This replaces
// the scattered per-component setInterval polls.
//
// It is deliberately minimal — the eventual win is pushing these over the SSE
// stream, but a shared cache already removes the duplicate-request / wasted
// background-poll problems for cheap.

type Channel<T = unknown> = {
  data: T | undefined;
  fetcher: () => Promise<T>;
  interval: number;
  subs: Set<() => void>;
  timer: ReturnType<typeof setInterval> | null;
  inflight: boolean;
};

const channels = new Map<string, Channel>();

function notify(ch: Channel) {
  ch.subs.forEach((fn) => fn());
}

async function runFetch(ch: Channel) {
  if (ch.inflight) return;
  ch.inflight = true;
  try {
    const data = await ch.fetcher();
    ch.data = data;
    notify(ch);
  } catch {
    /* keep last good value */
  } finally {
    ch.inflight = false;
  }
}

function startTimer(ch: Channel) {
  if (ch.timer != null || ch.interval <= 0) return;
  if (typeof document !== "undefined" && document.hidden) return; // don't poll in the background
  ch.timer = setInterval(() => runFetch(ch), ch.interval);
}

function stopTimer(ch: Channel) {
  if (ch.timer != null) {
    clearInterval(ch.timer);
    ch.timer = null;
  }
}

let visibilityBound = false;
function bindVisibility() {
  if (visibilityBound || typeof document === "undefined") return;
  visibilityBound = true;
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) {
      channels.forEach(stopTimer);
    } else {
      // Coming back: refresh once immediately, then resume polling.
      channels.forEach((ch) => {
        if (ch.subs.size > 0) {
          runFetch(ch);
          startTimer(ch);
        }
      });
    }
  });
}

// refreshResource forces an immediate refetch (e.g. right after creating an
// artifact) so dependent views update without waiting for the next tick.
export function refreshResource(key: string) {
  const ch = channels.get(key);
  if (ch) runFetch(ch);
}

interface Options {
  interval?: number; // ms; 0 = fetch once, no polling
  enabled?: boolean;
}

export function useResource<T>(key: string, fetcher: () => Promise<T>, opts: Options = {}): T | undefined {
  const { interval = 0, enabled = true } = opts;
  const [, force] = useState(0);

  useEffect(() => {
    if (!enabled || !key) return;
    let ch = channels.get(key) as Channel<T> | undefined;
    if (!ch) {
      ch = { data: undefined, fetcher, interval, subs: new Set(), timer: null, inflight: false };
      channels.set(key, ch as Channel);
    }
    ch.fetcher = fetcher; // keep the latest closure
    ch.interval = interval;
    const sub = () => force((x) => x + 1);
    ch.subs.add(sub);
    bindVisibility();
    if (ch.data === undefined) runFetch(ch as Channel); // first paint
    startTimer(ch as Channel);
    return () => {
      ch!.subs.delete(sub);
      if (ch!.subs.size === 0) stopTimer(ch as Channel); // keep cache warm for fast remount
    };
    // fetcher intentionally excluded: callers pass equivalent fetchers per key
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, interval, enabled]);

  return (channels.get(key) as Channel<T> | undefined)?.data;
}
