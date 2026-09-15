import { useCallback, useRef, useState } from 'react';
import type { ConversationSettings, SessionRecoveryStore } from '../lib/sessionRecovery';
const defaults = (): ConversationSettings => ({ enabledTools: [], selectedVersion: 'auto', activeSkill: null, confirmRisky: true });

export function useConversationSettings(session: SessionRecoveryStore, conversationID: string) {
  const records = useRef(new Map<string, ConversationSettings>());
  const [, render] = useState(0);
  const get = useCallback((cid: string) => {
    if (!records.current.has(cid)) records.current.set(cid, session.readSettings(cid) || defaults());
    return records.current.get(cid)!;
  }, [session]);
  const patch = useCallback((changes: Partial<ConversationSettings>) => {
    const next = { ...get(conversationID), ...changes };
    records.current.set(conversationID, next);
    session.writeSettings(conversationID, next);
    render(n => n + 1);
  }, [conversationID, get, session]);
  const move = useCallback((from: string, to: string) => {
    const value = get(from);
    records.current.set(to, value);
    records.current.set(from, defaults());
    session.writeSettings(to, value);
    session.writeSettings(from, defaults());
    render(n => n + 1);
  }, [get, session]);
  return { value: get(conversationID), patch, move };
}
