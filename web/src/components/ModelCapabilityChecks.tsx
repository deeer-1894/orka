import { ActionChip } from './ActionChip';
import type { Capability, ModelProfile } from '../lib/modelProfiles';
const labels: Record<Capability, string> = {
  text: '文本',
  vision: '图片',
  tools: '工具调用'
};
export function ModelCapabilityChecks({
  profile,
  disabled,
  dirty,
  onProbe
}: {
  profile: ModelProfile;
  disabled: boolean;
  dirty: boolean;
  onProbe: (model: string, capability: Capability) => void;
}) {
  if (profile.protocol !== 'openai-compatible') return <p className="text-sm text-muted">此协议当前尚未支持发现、检测和模型调用。可以保存配置。</p>;
  return <section aria-label="能力检测" className="space-y-2">
  <p className="text-sm text-muted">检测会实际调用模型，仅点击时执行，可能产生用量。{dirty ? '请先保存更改。' : ''}</p>
  {profile.models.filter(Boolean).map(model => <div key={model} className="rounded-lg border border-border p-3"><strong className="text-sm">{model}</strong>
   {(Object.keys(labels) as Capability[]).map(cap => {
        const check = profile.verified?.[model]?.[cap];
        return <div key={cap} className="mt-2 flex flex-wrap items-center justify-between gap-2 text-xs">
    <span>{labels[cap]}：{check ? check.verified ? '已验证' : '验证失败' : '未验证'}{check?.checked_at && ` · ${new Date(check.checked_at).toLocaleString()}`}{check?.error && ` · ${check.error}`}</span>
    <ActionChip type="button" disabled={disabled || dirty || !profile.enabled} aria-label={`检测 ${labels[cap]} ${model}`} onClick={() => onProbe(model, cap)}>检测{labels[cap]}</ActionChip>
   </div>;
      })}
  </div>)}
 </section>;
}
