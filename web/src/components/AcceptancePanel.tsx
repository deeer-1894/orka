import { ActionChip, actionChipClass } from './ActionChip';
import { useEffect, useState } from 'react';
import { api } from '../api';
import type { RunAcceptance } from '../lib/runEvidence';
const statusLabels = {
  passed: '通过',
  failed: '未通过',
  unverified: '未验证'
};
const displayValue = (value: unknown) => typeof value === 'string' ? value : JSON.stringify(value);
export default function AcceptancePanel({
  runID
}: {
  runID: string;
}) {
  const [data, setData] = useState<RunAcceptance>();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    let current = true;
    setLoading(true);
    setError('');
    setData(undefined);
    api.runAcceptance(runID).then(value => {
      if (current) setData(value);
    }).catch(e => {
      if (current) setError(e.message);
    }).finally(() => {
      if (current) setLoading(false);
    });
    return () => {
      current = false;
    };
  }, [runID, revision]);
  return <section aria-label="运行验收记录" className="space-y-2 rounded-lg border border-border p-3 text-xs">
    <div className="flex justify-between gap-2"><strong>验收记录</strong><ActionChip disabled={loading} onClick={() => setRevision(n => n + 1)} icon="refresh">刷新验收记录</ActionChip></div>
    {loading && <p role="status">正在读取验收记录…</p>}
    {error && <p role="alert" className="text-accent">无法读取验收记录：{error}</p>}
    {data && <>
      {!!data.executions?.length && <details><summary className={actionChipClass + " cursor-pointer list-none"}>实际执行记录 · {data.executions.length}</summary>
        <p className="py-2 text-muted">退出成功只证明该进程正常结束，不代表全部需求或页面交互已经验证。</p>
        {data.executions_truncated && <p>仅展示最近的执行记录。</p>}
        {data.executions.map(record => <article key={record.id} className="space-y-1 border-t border-border py-2">
          <strong>{record.timed_out ? '超时' : record.canceled ? '已取消' : record.ok ? '执行成功' : '执行失败'} · 退出码 {record.exit_code}</strong>
          <p className="break-all text-muted">{record.tool} · {new Date(record.at).toLocaleString()}</p>
          <pre className="whitespace-pre-wrap break-all">{record.command}</pre>
          {record.revision_scope === 'workspace_sample' && <p className="text-muted">工作区文件抽样，未登记交付清单；这些文件不代表正式交付物，也不代表全部被本命令验证。</p>}
          {!record.files?.length && <p className="text-muted">本记录未包含文件版本，不能判断测试对应哪份文件。</p>}
          {!!record.changed_during?.length && <p className="text-accent break-all">执行前后文件版本不同或已不可读取：{record.changed_during.join('、')}。本记录不能证明修改后的版本通过验收。</p>}
          {!!record.changed_since?.length && <p className="text-accent break-all">与执行时版本不同或已不可读取：{record.changed_since.join('、')}。旧结果不能作为当前版本的验收结论。</p>}
          {record.revisions_partial && <p>文件版本记录不完整，不能据此判定全部文件未变化。</p>}
          {!!record.files?.length && <details><summary className="cursor-pointer text-muted">执行时的文件版本</summary>{record.files.map(file => <p key={file.path} className="break-all">{file.path} · SHA-256 {file.sha256}</p>)}</details>}
          <pre className="whitespace-pre-wrap break-all">{record.stdout}{record.stderr}</pre>
          {record.error && <p className="break-all text-accent">{record.error}</p>}
          {record.output_truncated && <p>输出已截断，完整记录请查看对应执行步骤。</p>}
        </article>)}
      </details>}
      {!data.checks?.length && <p>尚未检查，不能判定验收通过。</p>}
      {data.contract?.truncated && <p>需求记录已截断，不能据此判定全部要求已满足。</p>}
      {!!data.contract?.requests?.length && <details><summary className={actionChipClass + " cursor-pointer list-none"}>任务要求</summary><ul>{data.contract.requests.map((request, i) => <li key={i} className="whitespace-pre-wrap break-words py-1">{displayValue(request)}</li>)}</ul></details>}
      {(data.checks || []).map((check, i) => {
        const results = check.report.results || [];
        const passed = check.report.ok && !check.report.error && results.length > 0 && results.every(result => result.status === 'passed');
        return <article key={`${check.at}:${i}`} className="space-y-2 border-t border-border pt-2">
          <p><strong>{passed ? '检查通过' : '检查未全部通过'}</strong> · {check.at}</p>
          <p className="break-all text-muted">范围：{check.report.scope} · 规则：{check.spec_path}</p>
          {check.report.error && <p className="text-accent">{check.report.error}</p>}
          {!results.length && <p>没有逐项检查结果。</p>}
          {results.map((result, j) => <div key={`${result.id}:${j}`} className="space-y-1 rounded bg-surface2 p-2">
            <strong>{statusLabels[result.status] || '未验证'} · {result.description || result.id}</strong>
            <p className="break-all text-muted">方法：{result.method}{result.file ? ` · 文件：${result.file}` : ''}</p>
            {result.actual !== undefined && <p className="whitespace-pre-wrap break-words">实际：{displayValue(result.actual)}</p>}
            {result.expected !== undefined && <p className="whitespace-pre-wrap break-words">预期：{displayValue(result.expected)}</p>}
            {result.sha256 && <p className="break-all">SHA-256：{result.sha256}</p>}
            {result.detail && <p className="whitespace-pre-wrap break-words">{result.detail}</p>}
          </div>)}
        </article>;
      })}
    </>}
  </section>;
}
