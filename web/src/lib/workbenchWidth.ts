const DEFAULT_WIDTH = 400;
type WidthStorage = Pick<Storage, 'getItem' | 'setItem'>;
export function widthBounds(viewport: number) {
  // Preserve usable chat space on desktop; fit small overlay viewports.
  const max = Math.min(720, Math.max(1, viewport < 768 ? viewport - 16 : viewport - 320));
  return { min: Math.min(360, max), max };
}
export function clampWidth(width: number, viewport: number) {
  const { min, max } = widthBounds(viewport);
  return Math.round(Math.min(max, Math.max(min, Number.isFinite(width) ? width : DEFAULT_WIDTH)));
}
export function keyboardWidth(key: string, width: number, viewport: number, shift = false): number | null {
  const { min, max } = widthBounds(viewport);
  const step = shift ? 60 : 20;
  if (key === 'Home') return min;
  if (key === 'End') return max;
  if (key === 'ArrowLeft') return clampWidth(width + step, viewport);
  if (key === 'ArrowRight') return clampWidth(width - step, viewport);
  return null;
}
const storageKey = (owner: string) => 'orka.workbench.width.v1:' + encodeURIComponent(owner);
export function readWidth(storage: WidthStorage | undefined, owner: string) {
  try {
    const value = Number(storage?.getItem(storageKey(owner)));
    return Number.isFinite(value) && value >= 360 && value <= 720 ? value : DEFAULT_WIDTH;
  } catch { return DEFAULT_WIDTH; }
}
export function saveWidth(storage: WidthStorage | undefined, owner: string, width: number) {
  try { storage?.setItem(storageKey(owner), String(clampWidth(width, 1440))); } catch { /* UI remains usable without storage. */ }
}
