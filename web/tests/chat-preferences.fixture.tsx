import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { SessionRecoveryStore } from '../src/lib/sessionRecovery';
import { useConversationSettings } from '../src/hooks/useConversationSettings';

function Composer({ owner }: { owner: string }) {
  const [session] = useState(() => new SessionRecoveryStore(owner));
  const [cid, setCID] = useState('');
  const settings = useConversationSettings(session, cid);
  return <>
    <output>{JSON.stringify({ cid, ...settings.value })}</output>
    <input aria-label="model" value={settings.value.selectedVersion} onChange={e => settings.patch({ selectedVersion: e.target.value })} />
    <button onClick={() => settings.patch({ confirmRisky: !settings.value.confirmRisky })}>Toggle confirmation</button>
    <button onClick={() => settings.patch({ enabledTools: ['browser'], activeSkill: 'test-skill' })}>Choose tools</button>
    <button onClick={() => { settings.move('', 'first'); setCID('first'); }}>Create first</button>
    <button onClick={() => setCID('')}>New chat</button>
    <button onClick={() => setCID('first')}>Open first</button>
  </>;
}
const owner = new URLSearchParams(location.search).get('owner') || 'one@example.com';
createRoot(document.getElementById('root')!).render(<Composer key={owner} owner={owner} />);
