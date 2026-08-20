export type SalesBIAsset = {
  path: string;
  filename: string;
  kind: "chart" | "report";
};

const CHART_TASK = /^sales-\d{8}-\d{6}-[0-9a-f]{6}$/;
const CHART_FILE = /^chart-\d{2}\.svg$/;
const REPORT_DIR = /^(daily|weekly|monthly)-\d{8}-[0-9a-f]{8,16}$/;

// Sales BI currently publishes local preview URLs on one of ten loopback ports.
// Only its two known path shapes are rewritten; unrelated localhost links keep
// their original behavior.
export function parseSalesBIAssetURL(raw: string): SalesBIAsset | null {
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    return null;
  }
  const host = parsed.hostname.replace(/^\[|\]$/g, "").toLowerCase();
  const port = Number(parsed.port);
  if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
      !["127.0.0.1", "localhost", "::1"].includes(host) ||
      !Number.isInteger(port) || port < 8765 || port > 8774) {
    return null;
  }

  let assetPath: string;
  try {
    assetPath = decodeURIComponent(parsed.pathname).replace(/^\/+/, "");
  } catch {
    return null;
  }
  if (!assetPath || assetPath.includes("\\") || assetPath.includes("\0")) return null;
  const parts = assetPath.split("/");
  const filename = parts[parts.length - 1] || "";

  if (parts.length === 3 && parts[0] === "query-charts" &&
      CHART_TASK.test(parts[1]) && CHART_FILE.test(filename)) {
    return { path: assetPath, filename, kind: "chart" };
  }
  if (parts.length >= 2 && REPORT_DIR.test(parts[0]) && /\.html?$/i.test(filename)) {
    return { path: assetPath, filename, kind: "report" };
  }
  return null;
}
