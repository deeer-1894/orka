// Keep popup permissions out of the untrusted iframe. A bootstrap installed
// before document scripts forwards only trusted HTTP(S) link clicks. Its nonce
// stays in a closure after the bootstrap removes itself from the document.
const NAVIGATION_KIND = 'orka:preview-link';

export function previewNavigationBootstrap(nonce: string): string {
  return `(() => {
    const nonce = ${JSON.stringify(nonce)}, kind = ${JSON.stringify(NAVIGATION_KIND)};
    const send = parent.postMessage.bind(parent), parseURL = URL;
    const closest = Element.prototype.closest, attribute = Element.prototype.getAttribute;
    document.currentScript.remove();
    document.addEventListener('click', event => {
      if (!event.isTrusted || event.defaultPrevented || event.button !== 0) return;
      const anchor = event.target instanceof Element ? closest.call(event.target, 'a[href]') : null;
      if (!anchor) return;
      let url;
      try { url = new parseURL(attribute.call(anchor, 'href')); } catch { return; }
      if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) return;
      event.preventDefault();
      send({ kind, nonce, url: url.href }, '*');
    }, true);
  })();`;
}

export function previewNavigationURL(event: MessageEvent, frame: Window | null, nonce: string): string | null {
  const data = event.data;
  if (!frame || event.source !== frame || !data || data.kind !== NAVIGATION_KIND || data.nonce !== nonce || typeof data.url !== 'string') return null;
  try {
    const url = new URL(data.url);
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : null;
  } catch { return null; }
}
