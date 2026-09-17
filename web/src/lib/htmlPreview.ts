// Preview documents never receive session credentials or a same-origin window.
// Resolve local dependencies in the parent, then embed only their bytes.
import { previewNavigationBootstrap } from './htmlPreviewNavigation';
export const HTML_PREVIEW_MAX_BYTES = 2_000_000;
const MAX_ASSETS = 32;
const MAX_ASSET_BYTES = 1_000_000;
const MAX_TOTAL_ASSET_BYTES = 4_000_000;
const CSP = "default-src 'none'; script-src 'unsafe-inline' data:; style-src 'unsafe-inline' data:; img-src data:; font-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; form-action 'none'; base-uri 'none'";

export function resolveHtmlAsset(documentPath: string, reference: string): string | null {
  if (!reference || /^(?:[a-z][a-z\d+.-]*:|\/\/|#)/i.test(reference.trim())) return null;
  let decoded: string;
  try { decoded = decodeURIComponent(reference.split(/[?#]/, 1)[0]); } catch { return null; }
  if (/[\\\0]/.test(decoded)) return null;
  const parts = decoded.startsWith('/') ? [] : documentPath.split('/').slice(0, -1);
  for (const part of decoded.split('/')) {
    if (part === '..') { if (!parts.length) return null; parts.pop(); }
    else if (part && part !== '.') parts.push(part);
  }
  return parts.join('/') || null;
}

function dataURL(bytes: Uint8Array, mime: string): string {
  let binary = '';
  for (let offset = 0; offset < bytes.length; offset += 8192) binary += String.fromCharCode(...bytes.subarray(offset, offset + 8192));
  return `data:${mime};base64,${btoa(binary)}`;
}
const IMAGE_MIME: Record<string, string> = { png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp', svg: 'image/svg+xml', ico: 'image/x-icon', woff: 'font/woff', woff2: 'font/woff2' };

export async function prepareHtmlPreview(html: string, path: string, fetchAsset: (path: string) => Promise<Response>, signal: AbortSignal, navigationNonce?: string) {
  const doc = new DOMParser().parseFromString(html, 'text/html');
  const skipped = new Set<string>();
  const cache = new Map<string, Uint8Array | null>();
  let totalBytes = 0;
  async function readAsset(assetPath: string) {
    signal.throwIfAborted();
    if (cache.has(assetPath)) { const cached = cache.get(assetPath); if (!cached) throw new Error("资源读取失败"); return cached; }
    if (cache.size >= MAX_ASSETS) throw new Error('资源数量超过预览范围');
    // Count failed requests too; a malformed page cannot issue unlimited reads.
    cache.set(assetPath, null);
    const response = await fetchAsset(assetPath);
    if (!response.ok || !response.body) throw new Error('无法读取资源');
    const reader = response.body.getReader();
    const chunks: Uint8Array[] = []; let length = 0;
    try {
      for (;;) {
        signal.throwIfAborted();
        const { done, value } = await reader.read(); if (done) break;
        length += value.length; totalBytes += value.length;
        if (length > MAX_ASSET_BYTES || totalBytes > MAX_TOTAL_ASSET_BYTES) throw new Error('资源大小超过预览范围');
        chunks.push(value);
      }
    } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
    const bytes = new Uint8Array(length); let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
    cache.set(assetPath, bytes); return bytes;
  }
  async function resource(ref: string, base: string, mime?: string): Promise<string> {
    if (ref.startsWith('data:') || ref.startsWith('#')) return ref;
    const resolved = resolveHtmlAsset(base, ref);
    if (!resolved) { skipped.add(ref); return ''; }
    try { return dataURL(await readAsset(resolved), mime || IMAGE_MIME[resolved.split('.').pop()?.toLowerCase() || ''] || 'application/octet-stream'); }
    catch { signal.throwIfAborted(); skipped.add(ref); return ''; }
  }
  async function cssResources(css: string, base: string) {
    // CSS imports are not followed recursively. Ordinary image/font URLs are.
    css = css.replace(/@import\s+(?:url\([^)]*\)|["'][^"']*["'])[^;]*;/gi, () => { skipped.add('CSS @import'); return ''; });
    const matches = [...css.matchAll(/url\(\s*(['"]?)(.*?)\1\s*\)/gi)];
    for (const match of matches.reverse()) {
      const replacement = await resource(match[2], base);
      css = css.slice(0, match.index) + `url("${replacement}")` + css.slice(match.index! + match[0].length);
    }
    return css;
  }
  doc.querySelectorAll('base, meta[http-equiv], iframe, object, embed').forEach(node => node.remove());
  for (const link of doc.querySelectorAll<HTMLLinkElement>('link')) {
    if (link.rel.toLowerCase() !== 'stylesheet') { link.remove(); continue; }
    const ref = link.getAttribute('href') || '', resolved = resolveHtmlAsset(path, ref);
    try {
      if (!resolved) throw new Error('外部样式');
      const css = await cssResources(new TextDecoder().decode(await readAsset(resolved)), resolved);
      link.href = dataURL(new TextEncoder().encode(css), 'text/css'); link.removeAttribute('integrity'); link.removeAttribute('crossorigin');
    } catch { signal.throwIfAborted(); skipped.add(ref); link.remove(); }
  }
  for (const style of doc.querySelectorAll('style')) style.textContent = await cssResources(style.textContent || '', path);
  for (const node of doc.querySelectorAll<HTMLElement>('[style]')) node.setAttribute('style', await cssResources(node.getAttribute('style') || '', path));
  for (const script of doc.querySelectorAll<HTMLScriptElement>('script[src]')) {
    script.src = await resource(script.getAttribute('src') || '', path, 'text/javascript');
    script.removeAttribute('integrity'); script.removeAttribute('crossorigin');
    if (!script.getAttribute('src')) script.remove();
  }
  for (const img of doc.querySelectorAll<HTMLImageElement>('img')) {
    img.removeAttribute('srcset'); img.src = await resource(img.getAttribute('src') || '', path);
  }
  // A srcset cannot retain same-origin URLs that could contain credentials.
  doc.querySelectorAll('source[srcset]').forEach(node => node.removeAttribute('srcset'));
  doc.querySelectorAll('a').forEach(node => node.setAttribute('rel', 'noreferrer noopener'));
  if (navigationNonce) {
    const bridge = doc.createElement('script');
    bridge.textContent = previewNavigationBootstrap(navigationNonce);
    doc.head.prepend(bridge);
  }
  const policy = doc.createElement('meta'); policy.httpEquiv = 'Content-Security-Policy'; policy.content = CSP;
  doc.head.prepend(policy);
  signal.throwIfAborted();
  return { document: '<!doctype html>\n' + doc.documentElement.outerHTML, skipped: [...skipped] };
}
