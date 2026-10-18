import { useEffect, useState } from 'react';
import { api } from '../api';
import type { RunBudget } from '../lib/runBudget';

export function useRunUsage(runID: string, enabled = true) {
  const [data, setData] = useState<RunBudget>();
  const [error, setError] = useState('');
  const [revision, setRefresh] = useState(0);
  useEffect(() => {
    let current = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    setData(undefined);
    setError('');
    if (!enabled) return;
    const load = async () => {
      try {
        const result = await api.runBudget(runID);
        if (!current) return;
        setData(result);
        setError('');
        if (result.status === 'running' || result.status === 'paused') timer = setTimeout(() => {
          if (current) void load();
        }, 5000);
      } catch (e) {
        if (current) { setError((e as Error).message); setData(undefined); timer = setTimeout(() => { if (current) void load(); }, 5000); }
      }
    };
    void load();
    return () => {
      current = false;
      clearTimeout(timer);
    };
  }, [runID, revision, enabled]);
  return { data, error, refresh: () => setRefresh(n => n + 1) };
}
