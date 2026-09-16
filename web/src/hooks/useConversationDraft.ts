import { useRef, useState } from 'react';
import type { DraftSnapshot, DraftAttachment, SessionRecoveryStore } from '../lib/sessionRecovery';
export type Attachment = DraftAttachment;
const empty = (): DraftSnapshot => ({ text: '', attachments: [] });

// The conversation owns composer edits. Storage is a reload checkpoint, with
// synchronous writes so navigation cannot race a delayed persistence effect.
export function useConversationDraft(conversationID: string, session?: SessionRecoveryStore) {
  const drafts = useRef(new Map<string, DraftSnapshot>());
  const [, render] = useState(0);
  const get = (cid: string) => {
    if (!drafts.current.has(cid)) drafts.current.set(cid, session?.readDraft(cid) || empty());
    return drafts.current.get(cid)!;
  };
  const patch = (cid: string, fn: (draft: DraftSnapshot) => DraftSnapshot) => {
    const next = fn(get(cid));
    drafts.current.set(cid, next);
    session?.writeDraft(cid, next);
    render(n => n + 1);
  };
  const draft = get(conversationID);
  const setText = (text: string | ((previous: string) => string)) => patch(conversationID, d => ({ ...d, text: typeof text === 'function' ? text(d.text) : text }));
  const setAttachments = (fn: (previous: Attachment[]) => Attachment[]) => patch(conversationID, d => ({ ...d, attachments: fn(d.attachments) }));
  const addAttachment = (a: Attachment) => patch(a.conversationID, d => ({ ...d, attachments: [...d.attachments.filter(item => item.path !== a.path), a] }));
  const clearAccepted = (cid: string, sent: Pick<DraftSnapshot, 'text' | 'attachments'>) => patch(cid, d => ({ ...d, text: d.text === sent.text ? '' : d.text, attachments: d.attachments.filter(a => !sent.attachments.includes(a)) }));
  const move = (from: string, to: string) => {
    const value = get(from);
    drafts.current.set(to, value); drafts.current.set(from, empty());
    session?.writeDraft(to, value); session?.removeDraft(from);
    render(n => n + 1);
  };
  return { ...draft, setText, setAttachments, addAttachment, clearAccepted, move, get };
}
