import { useEffect, useRef, useState } from "react";
import { useOverlay } from "./useOverlay";

// A promise-based confirm dialog that matches the app's pop-in modal + a11y
// (focus trap, aria-modal) instead of the OS-native confirm(). Mount <ConfirmHost/>
// once (next to <Toaster/>), then `await confirmDialog({ title, danger })`.

type ConfirmOpts = { title: string; body?: string; confirmText?: string; cancelText?: string; danger?: boolean };
type Pending = ConfirmOpts & { resolve: (ok: boolean) => void };

let push: ((p: Pending) => void) | null = null;

export function confirmDialog(opts: ConfirmOpts): Promise<boolean> {
  return new Promise((resolve) => {
    if (!push) { resolve(window.confirm(opts.title)); return; } // SSR / host not mounted
    push({ ...opts, resolve });
  });
}

export function ConfirmHost() {
  const [cur, setCur] = useState<Pending | null>(null);
  useEffect(() => {
    push = (p) => setCur(p);
    return () => { push = null; };
  }, []);
  if (!cur) return null;
  const done = (ok: boolean) => { cur.resolve(ok); setCur(null); };
  return <ConfirmModal opts={cur} onDone={done} />;
}

function ConfirmModal({ opts, onDone }: { opts: ConfirmOpts; onDone: (ok: boolean) => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useOverlay(() => onDone(false), ref);
  return (
    <div className="overlay-in fixed inset-0 z-[70] flex items-center justify-center bg-black/30 p-6" onClick={() => onDone(false)}>
      <div ref={ref} role="dialog" aria-modal="true" className="pop-in w-full max-w-sm rounded-2xl border border-border bg-surface p-4 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="text-[15px] font-medium text-ink">{opts.title}</div>
        {opts.body && <div className="mt-1 text-[13px] text-muted">{opts.body}</div>}
        <div className="mt-4 flex justify-end gap-2">
          <button onClick={() => onDone(false)} className="rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted hover:bg-surface2">
            {opts.cancelText || "取消"}
          </button>
          <button
            autoFocus
            onClick={() => onDone(true)}
            className={"rounded-lg px-3 py-1.5 text-[13px] text-white hover:opacity-90 " + (opts.danger ? "bg-[#d06363]" : "bg-accent")}
          >
            {opts.confirmText || "确定"}
          </button>
        </div>
      </div>
    </div>
  );
}
