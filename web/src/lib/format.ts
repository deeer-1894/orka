export const fmtTime = (ts: number) => {
  const d = new Date(ts);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  const ss = String(d.getSeconds()).padStart(2, "0");
  return `${hh}:${mm}:${ss}.${String(ts % 1000).padStart(3, "0")}`;
};

export const shortTrace = (t?: string) => (t ? t.slice(0, 8) : "········");

export const initials = (s: string) => {
  const base = (s || "?").split("@")[0];
  return base.slice(0, 2).toUpperCase();
};
