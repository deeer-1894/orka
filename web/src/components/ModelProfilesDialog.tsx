import { ActionChip, actionChipClass } from './ActionChip';
import { useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { useModelProfiles } from '../hooks/useModelProfiles';
import { exportProfiles, importProfiles } from '../lib/modelProfiles';
import { ModelProfileEditor } from './ModelProfileEditor';
import { ModelCapabilityChecks } from './ModelCapabilityChecks';
export default function ModelProfilesDialog({
  onClose,
  onSaved
}: {
  onClose: () => void;
  onSaved: () => void;
}) {
  const state = useModelProfiles();
  const [selected, setSelected] = useState(''),
    [json, setJson] = useState(''),
    [importError, setImportError] = useState('');
  const profile = state.data.profiles.find(p => p.id === (selected || state.data.active_profile_id)) || state.data.profiles[0];
  const busy = !!state.busy;
  return <Dialog.Root open onOpenChange={open => {
    if (!open && !busy) onClose();
  }}><Dialog.Portal><Dialog.Overlay className="fixed inset-0 z-50 bg-black/30" /><Dialog.Content className="fixed left-1/2 top-1/2 z-50 max-h-[90vh] w-[min(700px,calc(100vw-24px))] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-2xl border border-border bg-surface p-6 shadow-xl">
  <div className="flex items-center justify-between"><Dialog.Title className="text-xl">命名模型连接</Dialog.Title><ActionChip disabled={busy} onClick={onClose} aria-label="关闭命名连接" icon="close">关闭</ActionChip></div><Dialog.Description className="my-2 text-sm text-muted">保存多个账户与连接，活动连接用于下一次发送。</Dialog.Description>
  {!state.loaded ? busy ? <p role="status">正在读取连接…</p> : <ActionChip onClick={state.reload}>重新读取连接</ActionChip> : <form className="space-y-4" onSubmit={async e => {
          e.preventDefault();
          if (await state.save()) onSaved();
        }}>
   <fieldset disabled={busy} className="flex flex-wrap items-center gap-2">
    <label className="text-sm">编辑连接<select aria-label="编辑连接" value={profile?.id || ''} onChange={e => setSelected(e.target.value)} className="ml-2 rounded border border-border bg-bg p-2">{state.data.profiles.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></label>
    <ActionChip type="button" onClick={() => {
              const id = crypto.randomUUID();
              state.edit(d => ({
                active_profile_id: d.active_profile_id || id,
                profiles: [...d.profiles, {
                  id,
                  name: '新连接',
                  protocol: 'openai-compatible',
                  provider: 'custom',
                  base_url: '',
                  models: [],
                  enabled: false,
                  api_key_set: false
                }]
              }));
              setSelected(id);
            }} icon="plus">新增连接</ActionChip>
    {profile && <ActionChip type="button" disabled={state.data.profiles.length < 2} onClick={() => {
              state.edit(d => {
                const profiles = d.profiles.filter(p => p.id !== profile.id);
                return {
                  profiles,
                  active_profile_id: d.active_profile_id === profile.id ? profiles[0].id : d.active_profile_id
                };
              });
              setSelected('');
            }} icon="trash">删除此连接</ActionChip>}
   </fieldset>
   <label className="block text-sm">活动连接<select aria-label="活动连接" disabled={busy} value={state.data.active_profile_id} onChange={e => state.edit(d => ({
              ...d,
              active_profile_id: e.target.value
            }))} className="ml-2 rounded border border-border bg-bg p-2">{state.data.profiles.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></label>
   {profile && <><ModelProfileEditor profile={profile} disabled={busy} onChange={p => state.patch(profile.id, p)} onDiscover={() => void state.discover(profile)} /><ModelCapabilityChecks profile={profile} disabled={busy} dirty={state.dirty} onProbe={(m, c) => void state.probe(profile, m, c)} /></>}
   <details className="rounded-lg border border-border p-3"><summary className={actionChipClass + " cursor-pointer list-none"}>连接配置导入 / 导出</summary><p className="my-2 text-xs text-muted">导出不包含密钥或验证结果；兼容旧单连接配置。导入后需检查并保存。</p><textarea aria-label="连接 JSON 配置" value={json} onChange={e => setJson(e.target.value)} rows={5} className="w-full rounded border border-border bg-bg p-2 font-mono text-xs" /><div className="mt-2 flex gap-3"><ActionChip type="button" onClick={() => setJson(exportProfiles(state.data))} icon="download">导出连接配置</ActionChip><ActionChip type="button" disabled={busy} onClick={() => {
                try {
                  const imported = importProfiles(json);
                  state.edit(() => imported);
                  setSelected('');
                  setImportError('');
                } catch (e) {
                  setImportError((e as Error).message);
                }
              }} icon="check">应用连接 JSON</ActionChip></div></details>
   <ActionChip type="submit" disabled={busy || !state.data.profiles.length} icon="check">{state.busy === 'save' ? '正在保存…' : '保存所有连接'}</ActionChip>
  </form>}
  {state.notice && <p role="status" className="mt-3 text-sm text-muted">{state.notice}</p>}{(state.error || importError) && <p role="alert" className="mt-3 text-sm text-accent">{state.error || importError}</p>}
 </Dialog.Content></Dialog.Portal></Dialog.Root>;
}
