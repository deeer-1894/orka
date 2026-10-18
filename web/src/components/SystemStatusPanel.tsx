import { ActionChip } from './ActionChip';
import { useEffect, useState } from 'react';
import { api, type SystemStatus } from '../api';
export function SystemStatusPanel() {
  const [data, setData] = useState<SystemStatus>(),
    [error, setError] = useState(''),
    [loading, setLoading] = useState(true),
    [revision, setRevision] = useState(0);
  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError('');
    api.systemStatus().then(s => {
      if (alive) setData(s);
    }).catch(e => {
      if (alive) setError(e.message);
    }).finally(() => {
      if (alive) setLoading(false);
    });
    return () => {
      alive = false;
    };
  }, [revision]);
  return <section className="space-y-3 p-3" aria-label="服务状态"><div className="flex justify-between"><strong>服务状态</strong><ActionChip disabled={loading} onClick={() => setRevision(n => n + 1)} icon="refresh">刷新服务状态</ActionChip></div>{loading && <p role="status">正在读取服务状态…</p>}{error && <p role="alert" className="text-sm text-accent">{error}</p>}{data && <><p className="text-sm">{data.ready ? '服务已就绪' : '服务尚未全部就绪'}</p><p className="break-all text-xs text-muted">版本：{data.version}{data.modified ? '（含未提交修改）' : ''}</p><p className="text-xs text-muted">构建：{data.build_time || '未知'}<br />启动：{data.started_at}</p><ul className="space-y-2">{data.services.map(s => <li key={s.name} className="rounded-lg border border-border p-2 text-sm"><strong>{s.name} · {s.status}</strong><p className="break-words text-xs text-muted">{s.detail}</p></li>)}</ul><p className="text-xs text-muted">模型调用检测：{data.model_probe === 'not_run' ? '未执行' : data.model_probe}</p></>}</section>;
}
