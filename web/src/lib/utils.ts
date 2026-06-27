import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

// cn merges Tailwind classes intelligently (clsx for conditionals, tailwind-merge
// to resolve conflicts) — the standard helper every shadcn/ui component uses.
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
