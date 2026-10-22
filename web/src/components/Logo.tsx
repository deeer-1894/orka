import { useId } from "react";

// The Orka mark: an orca's swept-back dorsal fin rising from the curve of its
// back, white on the terracotta tile. Drawn on a 24-unit grid so it stays crisp from the 16px
// favicon to the 60px home tile; public/favicon.svg is the same drawing.
export function Logo({ size = 28, className = "", title }: { size?: number; className?: string; title?: string }) {
  const id = useId().replace(/:/g, "");
  return <svg width={size} height={size} viewBox="0 0 24 24" className={className} role={title ? "img" : undefined} aria-label={title} aria-hidden={title ? undefined : true}>
    <defs>
      <linearGradient id={`orka-${id}`} x1="0" y1="0" x2="1" y2="1">
        <stop offset="0" style={{ stopColor: "color-mix(in srgb, var(--color-accent) 72%, white)" }} />
        <stop offset="1" style={{ stopColor: "var(--color-accent)" }} />
      </linearGradient>
    </defs>
    <rect width="24" height="24" rx="6.5" fill={`url(#orka-${id})`} />
    <path d="M6.9 17.3C9.4 14.2 9.9 8.8 8.5 3.9c5 2.4 8.3 7.5 8.9 13.4Z" fill="#fff" />
    <path d="M3.2 19.2C7.9 16.2 16.1 16.2 20.8 19.2" fill="none" stroke="#fff" strokeWidth="1.8" strokeLinecap="round" />
  </svg>;
}
