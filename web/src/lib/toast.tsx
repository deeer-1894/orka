// Tiny toast system: a module-level emitter + a <Toaster/> rendered once at the
// app root. Anything (including api.ts) can call toast(msg) without prop drilling.
import { useEffect, useState } from "react";

export type ToastKind = "error" | "info" | "success";
export interface Toast {
  id: number;
  kind: ToastKind;
  msg: string;
}

let seq = 0;
const listeners = new Set<(t: Toast) => void>();
const lastShown = new Map<string, number>(); // dedupe identical msgs (e.g. polling failures)

export function toast(msg: string, kind: ToastKind = "info") {
  const now = Date.now();
  const prev = lastShown.get(msg);
  if (prev && now - prev < 3000) return; // suppress repeats within 3s
  lastShown.set(msg, now);
  const t: Toast = { id: ++seq, kind, msg };
  listeners.forEach((l) => l(t));
}
export const toastError = (msg: string) => toast(msg, "error");

const STYLE: Record<ToastKind, string> = {
  error: "border-accent/40 bg-accentsoft text-accent",
  info: "border-border bg-surface text-ink",
  success: "border-ok/40 bg-ok/10 text-ok",
};
const ICON: Record<ToastKind, string> = { error: "⚠", info: "ℹ", success: "✓" };

export function Toaster() {
  const [items, setItems] = useState<Toast[]>([]);
  useEffect(() => {
    const onToast = (t: Toast) => {
      setItems((xs) => [...xs, t]);
      setTimeout(() => setItems((xs) => xs.filter((x) => x.id !== t.id)), 4200);
    };
    listeners.add(onToast);
    return () => {
      listeners.delete(onToast);
    };
  }, []);
  return (
    <div className="pointer-events-none fixed bottom-4 right-4 z-[100] flex flex-col gap-2" role="status" aria-live="polite">
      {items.map((t) => (
        <div
          key={t.id}
          className={"rise pointer-events-auto flex max-w-sm items-start gap-2 rounded-xl border px-3.5 py-2.5 text-[13px] shadow-lg " + STYLE[t.kind]}
        >
          <span className="mt-px shrink-0">{ICON[t.kind]}</span>
          <span className="min-w-0 break-words">{t.msg}</span>
          <button
            onClick={() => setItems((xs) => xs.filter((x) => x.id !== t.id))}
            className="ml-1 shrink-0 text-faint hover:text-ink"
            aria-label="关闭提示"
          >
            ✕
          </button>
        </div>
      ))}
    </div>
  );
}
