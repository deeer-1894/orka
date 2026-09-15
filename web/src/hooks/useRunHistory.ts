import { useEffect, useRef, useState } from 'react';
import { api } from '../api';
import type { RunRecord } from '../types';
export function useRunHistory(revision: number, attention: boolean) {
 const [page, setPage] = useState(0), [reload, setReload] = useState(0);
 const [state, setState] = useState({ runs: [] as RunRecord[], loading: true, error: '', hasMore: false });
 const previous = useRef(new Map<string,string>());
 useEffect(() => {
  let alive = true;
  const load = async () => {
   setState(s => ({ ...s, loading: true, error: '' }));
   try {
    const response = await api.listRuns({ offset: page * 50, size: 50, ...(attention ? { statuses: ['failed','partial','interrupted','paused'] } : {}) });
    if (!alive) return;
    const runs = response.runs || [], fingerprint = runs.map(r => r.run_id).join(',');
    if (page && runs.length && previous.current.get(`${attention}:${page-1}`) === fingerprint) { setState(s => ({ ...s, loading: false, hasMore: false, error: '服务尚未支持更早记录分页，已保留当前记录' })); return; }
    previous.current.set(`${attention}:${page}`, fingerprint);
    setState({ runs: attention ? runs.filter(r => ['failed','partial','interrupted','paused'].includes(r.status)) : runs, loading: false, error: '', hasMore: response.has_more ?? runs.length === 50 });
   } catch (e) { if (alive) setState(s => ({ ...s, loading: false, error: e instanceof Error ? e.message : '无法读取运行记录' })); }
  };
  void load(); const timer = window.setInterval(() => { if (!document.hidden) void load(); }, 5000);
  return () => { alive = false; window.clearInterval(timer); };
 }, [page, attention, revision, reload]);
 return { ...state, page, setPage, refresh: () => setReload(n => n + 1) };
}
