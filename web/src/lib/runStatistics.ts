import type { RunRecord } from '../types';

// Run segments are not whole user tasks: a resume creates another record.
// Keep this calculation explicit rather than calling model termination success.
export function runStatistics(runs: Pick<RunRecord, 'status' | 'duration_ms'>[]) {
  const terminal = runs.filter(r => ['done', 'partial', 'failed', 'interrupted'].includes(r.status));
  const count = (status: string) => runs.filter(r => r.status === status).length;
  const durations = terminal.map(r => r.duration_ms).filter(n => Number.isFinite(n) && n > 0);
  return {
    done: count('done'), partial: count('partial'), failed: count('failed'),
    interrupted: count('interrupted'), paused: count('paused'), running: count('running'),
    ended: terminal.length,
    completionRate: terminal.length ? Math.round(count('done') / terminal.length * 100) : undefined,
    timedRuns: durations.length,
    averageSeconds: durations.length ? Math.round(durations.reduce((a, b) => a + b, 0) / durations.length / 1000) : undefined,
  };
}
