import { useEffect, useMemo, useState } from 'react';
import type { Message } from '../types';
import { files } from '../api';
import { existingSessionFiles, sessionFileCandidates, type FileContext } from '../lib/sessionFiles';

export function useSessionFiles(messages: Message[], context: FileContext, status: string, shared: boolean) {
  const candidates = useMemo(() => sessionFileCandidates(messages, context), [messages, context.conversationID, context.ownerEmail]);
  const evidence = [...messages].reverse().find(m => ['tool', 'plan', 'task', 'chat'].includes(m.type));
  const key = JSON.stringify([context.conversationID, context.ownerEmail, shared, status, evidence?.id, candidates]);
  const [found, setFound] = useState<{ key: string; paths: string[] }>({ key: '', paths: [] });
  useEffect(() => {
    let cancelled = false;
    if (!shared && context.conversationID && candidates.length) {
      existingSessionFiles(candidates, dir => files.list(dir, true)).then(paths => {
        if (!cancelled) setFound({ key, paths });
      });
    }
    return () => { cancelled = true; };
  }, [key]);
  // FileList is owner-only: do not intersect a shared thread with MY workspace.
  return !shared && found.key === key ? found.paths : [];
}
