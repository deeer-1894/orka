import { ActionChip, actionChipClass } from './ActionChip';
import { Icon } from './Icon';
import { useEffect, useState } from 'react';
import { api } from '../api';
import type { DeliverySnapshot } from '../lib/runEvidence';
import { useFileRevision } from '../hooks/useFileRevision';

export default function DeliverySnapshots({ conversationID }: { conversationID: string }) {
  const revision = useFileRevision(conversationID);
  const [refresh, setRefresh] = useState(0);
  const [data, setData] = useState<DeliverySnapshot[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  useEffect(() => {
    let current = true;
    setData([]); setError(''); setLoading(true);
    api.listDeliveries(conversationID).then(value => { if (current) setData(value.deliveries || []); })
      .catch(e => { if (current) setError(e.message); })
      .finally(() => { if (current) setLoading(false); });
    return () => { current = false; };
  }, [conversationID, revision, refresh]);
  return <section aria-label="交付快照列表" className="mb-4 space-y-2 rounded-lg border border-border p-3 text-xs">
    <p className="text-muted">交付时保存的文件副本，与下方当前工作区分开；工作区后续修改不会改变快照。</p>
    <ActionChip disabled={loading} onClick={() => setRefresh(n => n + 1)} icon="refresh">刷新交付快照</ActionChip>
    {loading && <p role="status">正在读取交付快照…</p>}
    {error && <p role="alert" className="text-accent">无法读取交付快照：{error}</p>}
    {!loading && !error && !data.length && <p>本会话尚无交付快照。</p>}
    {data.map(snapshot => <article key={`${snapshot.run_id}:${snapshot.version}`} className="space-y-2 border-t border-border pt-2">
      <strong>交付版本 {snapshot.version}</strong><p className="break-all text-muted">{new Date(snapshot.created_at).toLocaleString()} · {snapshot.run_id}</p>
      {snapshot.files.map(file => <div key={file.path} className="space-y-1">
        <p className="min-w-0 break-all">{file.path}</p>
        <a className={actionChipClass} aria-label={`下载快照 ${file.path}`} href={api.deliveryDownloadURL(conversationID, snapshot.run_id, file.path)}><Icon name="download" size={13} />下载</a>
        <p>{file.size.toLocaleString()} 字节</p><p className="break-all text-faint">SHA-256：{file.sha256}</p>
      </div>)}
    </article>)}
  </section>;
}
