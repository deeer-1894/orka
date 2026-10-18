import { useRef, useState } from 'react';
import { api } from '../api';
import { RunActions, type ResumeToolSelection } from '../lib/runActions';
import type { RunRecord } from '../types';
import { refreshResource } from '../lib/useResource';
export function useRunActions(attach: (cid: string) => unknown, selection?: ResumeToolSelection) {
 const [, update] = useState(0);
 const ref = useRef(attach); ref.current = attach;
 const selectionRef = useRef(selection); selectionRef.current = selection;
 const [actions] = useState(() => new RunActions({ list: async cid => (await api.listRuns({conversation_id:cid})).runs || [], get: api.getRun, resume: api.resumeRun }, () => update(n => n + 1), cid => selectionRef.current?.(cid)));
 const resume = async (run: RunRecord) => {
  const response = await actions.resume(run);
  if (response) { void ref.current(response.conversation_id); refreshResource('runs:all'); }
  return response;
 };
 return { actions, resume };
}
