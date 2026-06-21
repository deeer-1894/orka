import { useEffect } from "react";

// useOverlay gives every modal consistent behavior: Escape closes it, and the
// page behind it can't scroll while it's open. Call once at the top of a modal
// component with its close handler.
export function useOverlay(onClose: () => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
      }
    };
    document.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [onClose]);
}
