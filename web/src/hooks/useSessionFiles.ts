import { useEffect, useMemo, useState } from 'react';
import type { Message } from '../types';
import { files } from '../api';
import { existingSessionFiles, sessionFileCandidates, type FileContext } from '../lib/sessionFiles';

export function useSessionFiles(messages: Message[], context: FileContext, status: string) {
  const candidates = useMemo(() => sessionFileCandidates(messages, context), [messages, context.conversationID, context.ownerEmail]);
  const evidence = [...messages].reverse().find(m => ['tool', 'plan', 'task', 'chat'].includes(m.type));
  const key = JSON.stringify([context.conversationID, context.ownerEmail, status, evidence?.id, candidates]);
  const [found, setFound] = useState<{ key: string; paths: string[] }>({ key: '', paths: [] });
  useEffect(() => {
    let cancelled = false;
    if (context.conversationID && candidates.length) {
      existingSessionFiles(candidates, dir => files.scopedList(dir, context.conversationID, true)).then(paths => {
        if (!cancelled) setFound({ key, paths });
      });
    }
    return () => { cancelled = true; };
  }, [key]);
  return found.key === key ? found.paths : [];
}
