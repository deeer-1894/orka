// Minimal line-level diff (LCS) for the file-version history view. Good enough
// for showing what a write/restore changed; not a full Myers diff.
export type DiffRow = { type: "eq" | "add" | "del"; text: string };

export function lineDiff(oldText: string, newText: string): DiffRow[] {
  const a = oldText.split("\n");
  const b = newText.split("\n");
  const n = a.length;
  const m = b.length;
  // LCS length table.
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }
  const rows: DiffRow[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      rows.push({ type: "eq", text: a[i] });
      i++;
      j++;
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      rows.push({ type: "del", text: a[i++] });
    } else {
      rows.push({ type: "add", text: b[j++] });
    }
  }
  while (i < n) rows.push({ type: "del", text: a[i++] });
  while (j < m) rows.push({ type: "add", text: b[j++] });
  return rows;
}

// diffStats summarizes added/removed line counts for a compact label.
export function diffStats(rows: DiffRow[]): { add: number; del: number } {
  let add = 0;
  let del = 0;
  for (const r of rows) {
    if (r.type === "add") add++;
    else if (r.type === "del") del++;
  }
  return { add, del };
}
