
export const SESSION_PREFIX = 'orka.session.';
export const SESSION_SCHEMA = 1;
export const SESSION_TTL_MS = 24 * 60 * 60 * 1000;
export const SESSION_ENTRY_BYTES = 256 * 1024;
export const SESSION_TOTAL_BYTES = 2 * 1024 * 1024;
export const SESSION_MAX_ENTRIES = 32;
const OWNER_KEY = SESSION_PREFIX + 'owner';
let generation = 0;

export interface DraftAttachment { name: string; path: string; image: boolean; conversationID: string }
export interface DraftSnapshot { text: string; attachments: DraftAttachment[] }
export interface ConversationSettings { enabledTools: string[]; selectedVersion: string; activeSkill: string | null; confirmRisky: boolean }
export interface RetrySnapshot {
  message: string;
  conversationID: string;
  userEmail: string;
  enabledTools: string[];
  selectedVersion?: string;
  modelProfile?: string;
  activeSkill?: string;
  fileIDs?: string[];
  confirmRisky?: boolean;
}
type Kind = 'draft' | 'settings' | 'retry';
type Payload = DraftSnapshot | ConversationSettings | RetrySnapshot;
interface Envelope { version: number; owner: string; conversationID: string; kind: Kind; savedAt: number; expiresAt: number; payload: Payload }
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const strings = (value: unknown): value is string[] => Array.isArray(value) && value.length <= 256 && value.every(item => typeof item === 'string' && item.length <= 4096);
const bytes = (value: string) => new TextEncoder().encode(value).length;
function browserStorage() { try { return globalThis.sessionStorage; } catch { return undefined; } }
function keys(storage: Storage) { return Array.from({ length: storage.length }, (_, i) => storage.key(i)).filter((key): key is string => !!key && key.startsWith(SESSION_PREFIX)); }

export function clearSessionRecovery(storage = browserStorage()) {
  generation++; // Invalidates handles held by requests that finish after logout.
  try { if (storage) keys(storage).forEach(key => storage.removeItem(key)); } catch { /* storage may be disabled */ }
}
// Both writes and reads pass through the same whitelist. Never serialize an API
// config, auth token, arbitrary message metadata, callback or credential field.
function clean(kind: Kind, value: unknown, owner: string, cid: string): Payload | undefined {
  if (!object(value)) return;
  if (kind === 'draft') {
    if (typeof value.text !== 'string' || !Array.isArray(value.attachments) || value.attachments.length > 256) return;
    const attachments: DraftAttachment[] = [];
    for (const item of value.attachments) {
      if (!object(item) || item.conversationID !== cid || typeof item.name !== 'string' || typeof item.path !== 'string' || typeof item.image !== 'boolean') return;
      attachments.push({ name: item.name, path: item.path, image: item.image, conversationID: cid });
    }
    // Legacy budget fields are intentionally discarded; keep the user draft.
    return { text: value.text, attachments };
  }
  if (!strings(value.enabledTools)) return;
  if (kind === 'settings') {
    if (typeof value.selectedVersion !== 'string' || (value.activeSkill !== null && typeof value.activeSkill !== 'string') || typeof value.confirmRisky !== 'boolean') return;
    return { enabledTools: [...value.enabledTools], selectedVersion: value.selectedVersion, activeSkill: value.activeSkill, confirmRisky: value.confirmRisky };
  }
  if (value.userEmail !== owner || value.conversationID !== cid || typeof value.message !== 'string') return;
  const result: RetrySnapshot = { message: value.message, userEmail: owner, conversationID: cid, enabledTools: [...value.enabledTools] };
  for (const key of ['selectedVersion', 'modelProfile', 'activeSkill'] as const) {
    if (value[key] === undefined) continue;
    if (typeof value[key] !== 'string') return;
    result[key] = value[key];
  }
  if (value.fileIDs !== undefined) { if (!strings(value.fileIDs)) return; result.fileIDs = [...value.fileIDs]; }
  if (value.confirmRisky !== undefined) { if (typeof value.confirmRisky !== 'boolean') return; result.confirmRisky = value.confirmRisky; }
  return result;
}

/** One authenticated workbench owns one handle. Switching owners removes old
 * records; logout invalidates even already-running asynchronous writers. */
export class SessionRecoveryStore {
  private epoch: number;
  private warning = '';
  private listeners = new Set<() => void>();
  constructor(readonly owner: string, private storage = browserStorage(), private now: () => number = Date.now) {
    try {
      if (storage && owner) {
        if (storage.getItem(OWNER_KEY) !== owner) clearSessionRecovery(storage);
        storage.setItem(OWNER_KEY, owner);
      }
    } catch { this.storage = undefined; }
    this.epoch = generation;
    this.sweep();
  }
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  getWarning = () => this.warning;
  private warn() {
    this.warning = '部分内容无法跨刷新保存（存储不可用、已满或单条超过 256 KiB），当前页面仍保留原内容。';
    this.listeners.forEach(listener => listener());
  }
  private active() {
    try { return !!this.owner && this.epoch === generation && this.storage?.getItem(OWNER_KEY) === this.owner; } catch { return false; }
  }
  private key(kind: Kind, cid: string) { return `${SESSION_PREFIX}${encodeURIComponent(this.owner)}:${encodeURIComponent(cid)}:${kind}`; }
  private decode(raw: string, kind?: Kind, cid?: string): Envelope | undefined {
    if (bytes(raw) > SESSION_ENTRY_BYTES) return;
    try {
      const entry = JSON.parse(raw);
      const now = this.now();
      if (!object(entry) || entry.version !== SESSION_SCHEMA || entry.owner !== this.owner || typeof entry.conversationID !== 'string' || !['draft', 'retry', 'settings'].includes(String(entry.kind))) return;
      if ((kind && entry.kind !== kind) || (cid !== undefined && entry.conversationID !== cid)) return;
      if (typeof entry.savedAt !== 'number' || !Number.isFinite(entry.savedAt) || entry.savedAt > now || typeof entry.expiresAt !== 'number' || !Number.isFinite(entry.expiresAt) || entry.expiresAt <= now || entry.expiresAt > entry.savedAt + SESSION_TTL_MS) return;
      const payload = clean(entry.kind as Kind, entry.payload, this.owner, entry.conversationID);
      if (!payload) return;
      return { ...entry, payload } as unknown as Envelope;
    } catch { return; }
  }
  private sweep() {
    const entries: { key: string; size: number; savedAt: number }[] = [];
    if (!this.active() || !this.storage) return entries;
    try {
      for (const key of keys(this.storage)) {
        if (key === OWNER_KEY) continue;
        const raw = this.storage.getItem(key) || '';
        const entry = this.decode(raw);
        if (!entry || key !== this.key(entry.kind, entry.conversationID)) this.storage.removeItem(key);
        else entries.push({ key, size: bytes(key) + bytes(raw), savedAt: entry.savedAt });
      }
    } catch { /* read/write will surface storage failures */ }
    return entries.sort((a, b) => a.savedAt - b.savedAt);
  }
  private read(kind: Kind, cid: string): Payload | undefined {
    if (!this.active() || !this.storage) return;
    try {
      const key = this.key(kind, cid), raw = this.storage.getItem(key);
      if (!raw) return;
      const entry = this.decode(raw, kind, cid);
      if (!entry) this.storage.removeItem(key);
      return entry?.payload;
    } catch { return; }
  }
  private write(kind: Kind, cid: string, value: Payload) {
    // An invalidated writer must neither recreate the owner marker nor records.
    if (this.epoch !== generation) return false;
    if (!this.active() || !this.storage) { this.warn(); return false; }
    const key = this.key(kind, cid), payload = clean(kind, value, this.owner, cid);
    const savedAt = this.now();
    const raw = JSON.stringify({ version: SESSION_SCHEMA, owner: this.owner, conversationID: cid, kind, savedAt, expiresAt: savedAt + SESSION_TTL_MS, payload });
    try {
      if (!payload || bytes(raw) > SESSION_ENTRY_BYTES || bytes(key) > 4096) { this.storage.removeItem(key); this.warn(); return false; }
      const entries = this.sweep().filter(entry => entry.key !== key);
      let total = entries.reduce((sum, entry) => sum + entry.size, bytes(key) + bytes(raw));
      while (entries.length && (entries.length >= SESSION_MAX_ENTRIES || total > SESSION_TOTAL_BYTES)) {
        const oldest = entries.shift()!; this.storage.removeItem(oldest.key); total -= oldest.size;
      }
      this.storage.setItem(key, raw);
      return true;
    } catch {
      // Do not leave a previous draft/retry masquerading as the newest version.
      try { this.storage.removeItem(key); } catch { /* disabled */ }
      this.warn(); return false;
    }
  }
  removeDraft(cid: string) { if (this.active()) { try { this.storage?.removeItem(this.key('draft', cid)); } catch { /* disabled */ } } }
  readDraft(cid: string) { return this.read('draft', cid) as DraftSnapshot | undefined; }
  readSettings(cid: string) { return this.read('settings', cid) as ConversationSettings | undefined; }
  readRetry(cid: string) { return this.read('retry', cid) as RetrySnapshot | undefined; }
  writeDraft(cid: string, value: DraftSnapshot) { return this.write('draft', cid, value); }
  writeSettings(cid: string, value: ConversationSettings) { return this.write('settings', cid, value); }
  writeRetry(cid: string, value: RetrySnapshot) { return this.write('retry', cid, value); }
}
