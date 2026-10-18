import type { ConversationSettings } from './sessionRecovery';

export type ChatPreferences = Pick<ConversationSettings, 'selectedVersion' | 'confirmRisky'>;
const defaults: ChatPreferences = { selectedVersion: 'auto', confirmRisky: true };

function browserStorage(): Storage | undefined {
  try { return globalThis.localStorage; } catch { return undefined; }
}

function clean(value: unknown): Partial<ChatPreferences> {
  if (!value || typeof value !== 'object') return {};
  const data = value as Record<string, unknown>, result: Partial<ChatPreferences> = {};
  if (typeof data.selectedVersion === 'string' && data.selectedVersion.trim() && data.selectedVersion.length <= 256) {
    result.selectedVersion = data.selectedVersion;
  }
  if (typeof data.confirmRisky === 'boolean') result.confirmRisky = data.confirmRisky;
  return result;
}

// Account-scoped defaults are durable; drafts and conversation overrides remain
// in SessionRecoveryStore. Only these two explicit preferences are persisted.
export class ChatPreferencesStore {
  private readonly key: string;
  private value: ChatPreferences;

  constructor(owner: string, initial?: Partial<ChatPreferences>, private storage = browserStorage()) {
    this.key = `orka.chatPreferences.${encodeURIComponent(owner)}`;
    this.value = { ...defaults, ...clean(initial) };
    try {
      const raw = storage?.getItem(this.key);
      if (raw && raw.length <= 4096) this.value = { ...this.value, ...clean(JSON.parse(raw)) };
    } catch { /* Invalid/disabled storage must not break the composer. */ }
    this.save(); // Migrate the currently selected legacy conversation once.
  }

  read(): ChatPreferences { return { ...this.value }; }

  patch(changes: Partial<ConversationSettings>) {
    const preferences = clean(changes);
    if (!Object.keys(preferences).length) return;
    this.value = { ...this.value, ...preferences };
    this.save();
  }

  private save() {
    try { this.storage?.setItem(this.key, JSON.stringify(this.value)); }
    catch { /* The selection still works in memory when persistence is unavailable. */ }
  }
}
