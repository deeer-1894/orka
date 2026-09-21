import { useCallback, useRef, useState } from 'react';
import type { ConversationSettings, SessionRecoveryStore } from '../lib/sessionRecovery';
import { ChatPreferencesStore } from '../lib/chatPreferences';

export function useConversationSettings(session: SessionRecoveryStore, conversationID: string) {
  const [preferences] = useState(() => new ChatPreferencesStore(session.owner, session.readSettings(conversationID)));
  const records = useRef(new Map<string, ConversationSettings>());
  const [, render] = useState(0);
  const defaults = useCallback((): ConversationSettings => ({ enabledTools: [], activeSkill: null, ...preferences.read() }), [preferences]);
  const get = useCallback((cid: string) => {
    if (!records.current.has(cid)) records.current.set(cid, session.readSettings(cid) || defaults());
    const value = records.current.get(cid)!;
    // The blank composer follows the last explicit preference change, even if
    // it was already visited before changing a setting in another conversation.
    return cid ? value : { ...value, ...preferences.read() };
  }, [session, preferences, defaults]);
  const patch = useCallback((changes: Partial<ConversationSettings>) => {
    const next = { ...get(conversationID), ...changes };
    preferences.patch(changes);
    records.current.set(conversationID, next);
    session.writeSettings(conversationID, next);
    render(n => n + 1);
  }, [conversationID, get, session, preferences]);
  const move = useCallback((from: string, to: string) => {
    const value = get(from);
    records.current.set(to, value);
    records.current.set(from, defaults());
    session.writeSettings(to, value);
    session.writeSettings(from, defaults());
    render(n => n + 1);
  }, [get, session, defaults]);
  return { value: get(conversationID), patch, move };
}
