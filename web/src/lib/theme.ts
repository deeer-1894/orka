// Minimal theme controller: persists the user's choice and toggles the `dark`
// class on <html>, which flips the CSS variables defined in index.css.
import { useEffect, useState } from "react";

const KEY = "orka.theme";
export type Theme = "light" | "dark";

function initial(): Theme {
  const saved = localStorage.getItem(KEY);
  if (saved === "light" || saved === "dark") return saved;
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function apply(t: Theme) {
  document.documentElement.classList.toggle("dark", t === "dark");
}

// Apply as early as possible to avoid a flash.
apply(initial());

export function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(initial);
  useEffect(() => {
    apply(theme);
    localStorage.setItem(KEY, theme);
  }, [theme]);
  return [theme, () => setTheme((t) => (t === "dark" ? "light" : "dark"))];
}
