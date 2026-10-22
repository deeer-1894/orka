import { useId } from "react";

// The Orka mark: an orca's swept-back dorsal fin rising from the curve of its
// back, white on the terracotta tile, with about four units of margin on every
// side of the 24-unit grid. One drawing serves every size — the 16px favicon
// (public/favicon.svg), message avatars, the sidebar and the 60px home tile.
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
    <path d="M7.6 16.8C9.8 14 10.3 9.4 9.2 5.2c4.6 2.2 7.6 6.6 8.2 11.6Z" fill="#fff" />
    <path d="M4.4 18.7C8.6 16 15.4 16 19.6 18.7" fill="none" stroke="#fff" strokeWidth="1.7" strokeLinecap="round" />
  </svg>;
}
