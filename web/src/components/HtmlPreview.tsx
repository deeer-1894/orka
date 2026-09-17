import { useEffect, useRef, useState } from 'react';
import { auth, files } from '../api';
import { prepareHtmlPreview } from '../lib/htmlPreview';
import { previewNavigationURL } from '../lib/htmlPreviewNavigation';

export function HtmlPreview({ text, name, conv }: { text: string; name: string; conv: string }) {
  const [preview, setPreview] = useState<{ document: string; skipped: string[] } | null>(null);
  const [error, setError] = useState('');
  const frame = useRef<HTMLIFrameElement>(null);
  useEffect(() => {
    const controller = new AbortController(); setPreview(null); setError('');
    const nonce = crypto.randomUUID();
    const navigate = (event: MessageEvent) => {
      const url = previewNavigationURL(event, frame.current?.contentWindow || null, nonce);
      if (url) window.open(url, '_blank', 'noopener,noreferrer');
    };
    window.addEventListener('message', navigate);
    prepareHtmlPreview(text, name, path => fetch(files.downloadURL(path, conv), { headers: { Authorization: 'Bearer ' + auth.token() }, signal: controller.signal }), controller.signal, nonce)
      .then(result => { if (!controller.signal.aborted) setPreview(result); })
      .catch(error => { if (!controller.signal.aborted) setError(String(error)); });
    return () => { controller.abort(); window.removeEventListener('message', navigate); };
  }, [text, name, conv]);
  if (error) return <p role="alert" className="p-4 text-sm text-accent">无法加载页面：{error}</p>;
  if (!preview) return <p role="status" className="p-4 text-sm text-muted">正在加载页面…</p>;
  return <>
    {preview.skipped.length > 0 && <p role="status" className="border-b border-border px-4 py-2 text-xs text-muted">部分资源未加载（{preview.skipped.length} 项）。预览支持会话内的图片、样式和脚本；外部网络资源不会加载。</p>}
    <iframe ref={frame} title={`HTML 页面：${name}`} sandbox="allow-scripts" referrerPolicy="no-referrer" srcDoc={preview.document} className="h-[70vh] min-h-[280px] w-full border-0 bg-white" />
  </>;
}
