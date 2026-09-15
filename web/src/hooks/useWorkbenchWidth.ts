import { useEffect, useRef, useState, type HTMLAttributes } from 'react';
import { clampWidth, keyboardWidth, readWidth, saveWidth, widthBounds } from '../lib/workbenchWidth';
function storage() { try { return window.localStorage; } catch { return undefined; } }
export function useWorkbenchWidth(owner: string) {
  const [preferred, setPreferred] = useState(() => readWidth(storage(), owner));
  const [viewport, setViewport] = useState(() => window.innerWidth);
  const [dragging, setDragging] = useState(false);
  const drag = useRef<{ x: number; width: number; pointer: number } | null>(null);
  useEffect(() => { saveWidth(storage(), owner, preferred); }, [owner, preferred]);
  useEffect(() => {
    const resize = () => { setViewport(window.innerWidth); drag.current = null; setDragging(false); };
    window.addEventListener('resize', resize);
    return () => window.removeEventListener('resize', resize);
  }, []);
  const width = clampWidth(preferred, viewport);
  const bounds = widthBounds(viewport);
  const separator: HTMLAttributes<HTMLDivElement> = {
    role: 'separator', tabIndex: 0, 'aria-label': '调整工作台宽度', 'aria-orientation': 'vertical',
    'aria-valuemin': bounds.min, 'aria-valuemax': bounds.max, 'aria-valuenow': width,
    'aria-valuetext': `${width} 像素`,
    title: '向左拖动拉宽；左右方向键调整，Shift 加速，Home/End 最窄/最宽',
    onKeyDown: event => {
      const next = keyboardWidth(event.key, width, viewport, event.shiftKey);
      if (next !== null) { event.preventDefault(); setPreferred(next); }
    },
    onPointerDown: event => {
      if (event.button !== 0) return;
      event.preventDefault(); event.currentTarget.focus(); event.currentTarget.setPointerCapture(event.pointerId);
      drag.current = { x: event.clientX, width, pointer: event.pointerId }; setDragging(true);
    },
    onPointerMove: event => {
      const start = drag.current;
      if (start?.pointer === event.pointerId) setPreferred(clampWidth(start.width + start.x - event.clientX, viewport));
    },
    onPointerUp: event => {
      drag.current = null; setDragging(false);
      if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    },
    onLostPointerCapture: () => { drag.current = null; setDragging(false); },
    onPointerCancel: () => { drag.current = null; setDragging(false); },
  };
  return { width, dragging, separator };
}
