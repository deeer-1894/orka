import { ActionChip } from './ActionChip';
import type { ModelProfile } from '../lib/modelProfiles';
const input = 'w-full rounded-lg border border-border bg-bg px-3 py-2 text-sm';
export function ModelProfileEditor({
  profile,
  disabled,
  onChange,
  onDiscover
}: {
  profile: ModelProfile;
  disabled: boolean;
  onChange: (patch: Partial<ModelProfile>) => void;
  onDiscover: () => void;
}) {
  return <fieldset disabled={disabled} className="space-y-3">
  <label className="block text-sm">连接名称<input className={input} required value={profile.name} onChange={e => onChange({
        name: e.target.value
      })} /></label>
  <label className="block text-sm">协议<select className={input} value={profile.protocol} onChange={e => onChange({
        protocol: e.target.value
      })}><option value="openai-compatible">OpenAI 兼容</option></select></label>
  <label className="block text-sm">厂商标识<input className={input} value={profile.provider} onChange={e => onChange({
        provider: e.target.value
      })} /></label>
  <label className="block text-sm">连接地址<input className={input} type="url" required={profile.enabled} value={profile.base_url} onChange={e => onChange({
        base_url: e.target.value
      })} /></label>
  <label className="block text-sm">连接密钥<input className={input} type="password" autoComplete="new-password" value={profile.api_key || ''} placeholder={profile.api_key_set ? '已保存，留空保留' : '本地服务可留空'} onChange={e => onChange({
        api_key: e.target.value
      })} /></label>
  <label className="block text-sm">连接模型列表<textarea className={input} required={profile.enabled} rows={3} value={profile.models.join('\n')} onChange={e => onChange({
        models: e.target.value.split(/[\n,]/)
      })} /></label>
  <p className="text-xs text-muted">Auto 使用活动连接的第一个模型。发现列表不代表已验证调用权限。</p>
  <label className="flex gap-2 text-sm"><input type="checkbox" checked={profile.enabled} onChange={e => onChange({
        enabled: e.target.checked
      })} />启用此连接</label>
  <ActionChip type="button" onClick={onDiscover} disabled={!profile.base_url || profile.protocol !== 'openai-compatible'} icon="search">发现此连接模型</ActionChip>
 </fieldset>;
}
