// authorizeInPopup runs one-click connector authorization in a popup. The
// popup is opened synchronously on the click (so blockers allow it), then
// pointed at the provider once the server returns the URL; the callback page
// reports back with postMessage from this origin. Closing the popup ends it.
export interface OAuthResult { ok: boolean; error?: string; name?: string }

export async function authorizeInPopup(start: () => Promise<string>): Promise<OAuthResult> {
  const popup = window.open("about:blank", "orka-connector-oauth", "width=520,height=720");
  let url: string;
  try { url = await start(); } catch (e) { popup?.close(); return { ok: false, error: (e as Error).message || "无法开始授权" }; }
  if (!popup) return { ok: false, error: "浏览器拦截了授权窗口，请允许弹出窗口后重试" };
  popup.location.href = url;
  return new Promise(resolve => {
    const done = (r: OAuthResult) => { window.removeEventListener("message", onMessage); window.clearInterval(timer); resolve(r); };
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== window.location.origin || (e.data as { type?: string })?.type !== "orka-connector-oauth") return;
      const d = e.data as { ok: boolean; error?: string; name?: string };
      done({ ok: !!d.ok, error: d.error, name: d.name });
    };
    window.addEventListener("message", onMessage);
    const timer = window.setInterval(() => { if (popup.closed) done({ ok: false, error: "" }); }, 500);
  });
}
