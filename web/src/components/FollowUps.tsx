import { useEffect, useState } from 'react';
import { api } from '../api';
import { ActionChip } from './ActionChip';
interface Suggestions { items: string[]; retryAfter?: number }
const cache = new Map<string, Suggestions>();
const pending = new Map<string, Promise<Suggestions>>();
let generation = 0;
export function clearFollowUps() { generation++; cache.clear(); pending.clear(); }
interface FollowUpProps {
  ownerEmail: string;
  conversationID: string;
  runID: string;
  prompt: string;
  answer: string;
  selectedVersion: string;
  modelProfile: string;
  onPick: (text: string) => void;
}
export function FollowUps(props: FollowUpProps) {
  const key = JSON.stringify([props.ownerEmail, props.conversationID, props.runID, props.modelProfile, props.selectedVersion, props.prompt, props.answer]);
  return <AutomaticSuggestions key={key} {...props} cacheKey={key} />;
}
function AutomaticSuggestions({ ownerEmail, conversationID, runID, prompt, answer, selectedVersion, modelProfile, onPick, cacheKey }: FollowUpProps & { cacheKey: string }) {
  const [result, setResult] = useState(() => cache.get(cacheKey));
  const [settlementWake, setSettlementWake] = useState(0);
  useEffect(() => {
    let current = true;
    if (!ownerEmail || !conversationID || !runID || !answer.trim() || !modelProfile) return;
    const cached = cache.get(cacheKey);
    if (cached && (!cached.retryAfter || Date.now() < cached.retryAfter)) { setResult(cached); return; }
    const settlementRetry = !!cached?.retryAfter;
    const epoch = generation;
    let request = pending.get(cacheKey);
    if (!request) {
      request = (async () => {
        for (let attempt = 0; ; attempt++) {
          if (epoch !== generation) return { suggestions: [] };
          try { return await api.followups(prompt, answer, selectedVersion, modelProfile, conversationID, runID); }
          catch (error) {
            // A terminal SSE event can precede the persisted run by a moment.
            // Only retry conflicts: no provider call occurred for this response.
            if ((error as { status?: number }).status !== 409 || attempt >= (settlementRetry ? 0 : 2)) throw error;
            await new Promise(resolve => setTimeout(resolve, attempt === 0 ? 250 : 750));
          }
        }
      })()
        .then(response => ({ items: response.suggestions || [] }))
        .catch(error => ({ items: [], ...(!settlementRetry && (error as { status?: number }).status === 409 ? { retryAfter: Date.now() + 5000 } : {}) }));
      pending.set(cacheKey, request);
    }
    const ownRequest = request;
    void request.then(value => {
      if (epoch !== generation) return;
      cache.set(cacheKey, value);
      if (cache.size > 50) cache.delete(cache.keys().next().value!);
      if (current) setResult(value);
      if (pending.get(cacheKey) === ownRequest) pending.delete(cacheKey);
    });
    return () => { current = false; };
  }, [ownerEmail, conversationID, runID, prompt, answer, selectedVersion, modelProfile, cacheKey, settlementWake]);
  useEffect(() => {
    if (!result?.retryAfter) return;
    const timer = setTimeout(() => setSettlementWake(value => value + 1), Math.max(0, result.retryAfter - Date.now()));
    return () => clearTimeout(timer);
  }, [result?.retryAfter]);
  if (!result?.items.length) return null;
  return <div className="mb-6 ml-[42px] flex flex-wrap gap-2" aria-label="追问建议">
    {result.items.map(question => <ActionChip key={question} icon="sparkle" onClick={() => onPick(question)} title={question} className="max-w-full">
      <span className="truncate">{question}</span>
    </ActionChip>)}
  </div>;
}
