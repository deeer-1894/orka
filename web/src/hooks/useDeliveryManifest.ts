import { useEffect, useState } from 'react';
import { api } from '../api';
import type { DeliverySnapshot } from '../lib/runEvidence';
import { useFileRevision } from './useFileRevision';

// One manifest request per conversation/settled revision, never one per link.
// A run id must come from that message; the newest delivery is not a fallback.
export function useDeliveryManifest(conversationID: string, runKey: string, status: string) {
  const revision = useFileRevision(conversationID);
  const key = `${conversationID}:${runKey}:${status}:${revision}`;
  const [state, setState] = useState<{ key: string; snapshots: DeliverySnapshot[] }>({ key: '', snapshots: [] });
  useEffect(() => {
    let current = true;
    if (conversationID && runKey) {
      api.listDeliveries(conversationID).then(data => {
        if (current) setState({ key, snapshots: (data.deliveries || []).filter(item => item.conversation_id === conversationID) });
      }).catch(() => { if (current) setState({ key, snapshots: [] }); });
    }
    return () => { current = false; };
  }, [key]);
  return state.key === key ? state.snapshots : [];
}
