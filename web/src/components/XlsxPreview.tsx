import { ActionChip } from './ActionChip';
import { useEffect, useState } from 'react';
import type { SheetPreview } from '../workers/xlsxPreview.worker';
const MAX_BYTES = 8 * 1024 * 1024;
async function readWorkbook(response: Response) {
  if (!response.ok) throw new Error(`读取失败 (${response.status})`);
  if (Number(response.headers.get('content-length')) > MAX_BYTES) throw new Error('工作簿超过 8 MB，请下载查看。');
  const reader = response.body?.getReader();
  if (!reader) throw new Error('工作簿为空');
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const {
        done,
        value
      } = await reader.read();
      if (done) break;
      size += value.length;
      if (size > MAX_BYTES) {
        await reader.cancel();
        throw new Error('工作簿超过 8 MB，请下载查看。');
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  const buffer = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    buffer.set(chunk, offset);
    offset += chunk.length;
  }
  return buffer.buffer;
}
export default function XlsxPreview({
  url
}: {
  url: string;
}) {
  const [sheets, setSheets] = useState<SheetPreview[]>([]),
    [selected, setSelected] = useState(''),
    [error, setError] = useState(''),
    [loading, setLoading] = useState(true),
    [limited, setLimited] = useState(false);
  useEffect(() => {
    const abort = new AbortController();
    let worker: Worker | undefined;
    let timer: number | undefined;
    const load = async () => {
      try {
        const buffer = await readWorkbook(await fetch(url, {
          signal: abort.signal
        }));
        if (abort.signal.aborted) return;
        worker = new Worker(new URL('../workers/xlsxPreview.worker.ts', import.meta.url), {
          type: 'module'
        });
        timer = window.setTimeout(() => {
          worker?.terminate();
          if (!abort.signal.aborted) {
            setError('工作簿解析超时，请下载查看。');
            setLoading(false);
          }
        }, 15000);
        worker.onmessage = e => {
          if (abort.signal.aborted) return;
          window.clearTimeout(timer);
          worker?.terminate();
          setLoading(false);
          if (e.data.error) setError(e.data.error);else {
            setSheets(e.data.sheets);
            setSelected(e.data.sheets[0]?.name || '');
            setLimited(e.data.sheetsTruncated);
          }
        };
        worker.onerror = () => {
          window.clearTimeout(timer);
          worker?.terminate();
          if (!abort.signal.aborted) {
            setError('工作簿解析失败，请下载查看。');
            setLoading(false);
          }
        };
        worker.postMessage(buffer, [buffer]);
      } catch (e) {
        if (!abort.signal.aborted) {
          setError((e as Error).message);
          setLoading(false);
        }
      }
    };
    void load();
    return () => {
      abort.abort();
      window.clearTimeout(timer);
      worker?.terminate();
    };
  }, [url]);
  const sheet = sheets.find(s => s.name === selected);
  if (error) return <p role="alert" className="text-sm text-accent">{error}</p>;
  if (loading) return <p role="status" className="text-sm text-muted">正在读取工作簿…</p>;
  return <div className="space-y-3">
  <div role="tablist" aria-label="工作表" className="flex flex-wrap gap-2">{sheets.map(s => <ActionChip key={s.name} role="tab" aria-selected={selected === s.name} onClick={() => setSelected(s.name)}>{s.name}</ActionChip>)}</div>
  <p className="text-xs text-muted">公式显示文件中保存的缓存结果，不重新计算。</p>
  {sheet && <div className="max-h-[50vh] overflow-auto"><table className="w-full border-collapse text-left text-xs"><thead><tr><th className="border border-border p-2">行</th>{sheet.columns.map(c => <th key={c} className="border border-border p-2">{c}</th>)}</tr></thead><tbody>{sheet.rows.map(row => <tr key={row.number}><th className="border border-border p-2">{row.number}</th>{row.cells.map((cell, i) => <td key={i} title={cell.formula ? `公式：=${cell.formula}` : undefined} className="max-w-xs whitespace-pre-wrap break-words border border-border p-2">{cell.text}</td>)}</tr>)}</tbody></table></div>}
  {(sheet?.truncated || limited) && <p role="status" className="text-xs text-muted">预览已截断：最多 50 个工作表，每表 201 行、30 列。下载查看完整文件。</p>}
 </div>;
}
