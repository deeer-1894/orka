import React from 'react';
import { createRoot } from 'react-dom/client';
import { TaskEditor, draftFromTask, type TaskDraft } from '../src/components/tasks/TaskEditor';
import type { TaskMeta } from '../src/types';

// ?task=<json> opens an existing task; otherwise a new event task.
const raw = new URLSearchParams(location.search).get('task');
const draft: TaskDraft = raw ? draftFromTask(JSON.parse(raw) as TaskMeta)
  : { title: '', prompt: '总结新 issue', trigger: { kind: 'event' }, enabled: true, retry_count: 0 };
createRoot(document.getElementById('root')!).render(
  <TaskEditor draft={draft} onClose={() => { document.title = 'closed'; }} onSaved={() => {}} />);
