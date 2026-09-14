interface ModelSettingsInput {
  provider: string; base_url: string;
  models: string[]; enabled: boolean; api_key?: string;
}

// Portable profiles deliberately exclude credentials and retired routing roles.
export function exportModelProfile(profile: ModelSettingsInput): string {
  const { provider, base_url, models, enabled } = profile;
  return JSON.stringify({ version: 2, provider, base_url, models, enabled }, null, 2);
}

export function importModelProfile(text: string): ModelSettingsInput {
  const value: unknown = JSON.parse(text);
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("配置必须是 JSON 对象");
  const p = value as Record<string, unknown>;
  if (typeof p.base_url !== "string") throw new Error("配置需要 base_url 字段");
  const enabled = p.enabled !== false;
  if (p.base_url || enabled) {
    const url = new URL(p.base_url);
    if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error("Base URL 格式无效");
  }
  if (p.models !== undefined && (!Array.isArray(p.models) || p.models.some(m => typeof m !== "string"))) throw new Error("models 必须是模型名称数组");
  // Legacy profiles preserve their default, but never restore tiered routing.
  const legacyDefault = p.version !== 2 && typeof p.model === "string" ? [p.model] : [];
  const models = [...new Set([...legacyDefault, ...(Array.isArray(p.models) ? p.models as string[] : [])].map(m => m.trim()).filter(Boolean))];
  if (enabled && !models.length) throw new Error("请至少填写一个模型");
  return {
    provider: typeof p.provider === "string" ? p.provider : "custom",
    base_url: p.base_url.trim(), models, enabled,
  };
}
