"""Bounded execution evidence, independent of planner replies and browser handles.

Receipts mean an operator call returned, not that the task passed acceptance.
Observations are separately ordered snapshots; they may precede an action.
"""
from __future__ import annotations

import json
from typing import Any

MAX_EVENTS = 24
FIELD_LIMIT = 256
OBSERVATION_LIMIT = 1200


class Evidence:
    def __init__(self):
        self.events: list[dict[str, Any]] = []
        self.sequence = 0
        self.secrets: set[str] = set()

    def clean(self, value: Any, limit: int = FIELD_LIMIT) -> str:
        text = str(value)
        for secret in sorted(self.secrets, key=len, reverse=True):
            text = text.replace(secret, '[redacted]')
        return text if len(text) <= limit else text[:limit] + '…[truncated]'

    async def prepare(self, operator, action: dict[str, Any]) -> dict[str, Any]:
        # Capture the input classification BEFORE fill/Enter can detach its target.
        private = False
        if action.get('action') == 'type':
            try:
                private = bool(action.get('sensitive')) or await operator.input_is_sensitive(action)
            except Exception:
                private = True
            if private and action.get('text'):
                self.secrets.add(str(action['text']))
                if str(action['text']).rstrip('\n'):
                    self.secrets.add(str(action['text']).rstrip('\n'))
        receipt: dict[str, Any] = {'kind': 'action', 'action': self.clean(action.get('action', ''))}
        if private:
            receipt.update(input_redacted=True, target='[redacted]')
        else:
            kind = action.get('action')
            target = ''
            fields: tuple[str, ...] = ()
            if kind == 'navigate':
                target = action.get('url', '')
            elif kind in ('click', 'type'):
                if action.get('_handle') is not None:
                    target = action.get('target', '')
                    fields = ('mark',)
                elif kind == 'click' and 'x' in action:
                    fields = ('x', 'y', 'button', 'count')
                else:
                    target = action.get('selector') or action.get('target') or ''
                if kind == 'type':
                    fields += ('text',)
            elif kind == 'drag':
                fields = ('x', 'y', 'x2', 'y2')
            elif kind == 'scroll':
                fields = ('x', 'y', 'direction') if action.get('direction') else ('x', 'y', 'dy')
            elif kind == 'hotkey':
                fields = ('key',)
            if target:
                receipt['target'] = self.clean(target)
            # Only parameters used by this operator branch; no model extras,
            # _handle, _raw or _thought can become execution evidence.
            for key in fields:
                if key in action:
                    value = action[key]
                    receipt[key] = value if isinstance(value, (int, float)) else self.clean(value)
        if action.get('action') == 'type':
            keyboard = not (action.get('_handle') is not None or action.get('selector') or action.get('target'))
            receipt['input_mode'] = 'keyboard' if keyboard else 'fill'
            if keyboard and not private:
                text = str(action.get('text', ''))
                receipt['text'] = self.clean(text.rstrip('\n'))
                receipt['submit'] = text.endswith('\n')
        return receipt

    def append(self, step: int, event: dict[str, Any]) -> dict[str, Any]:
        self.sequence += 1
        entry = {'seq': self.sequence, 'step': step, **event}
        self.events = (self.events + [entry])[-MAX_EVENTS:]
        return entry

    def executed(self, step: int, receipt: dict[str, Any], result: str) -> dict[str, Any]:
        return self.append(step, {**receipt, 'result': '[redacted]' if receipt.get('input_redacted') else self.clean(result)})

    def observe(self, step: int, content: str) -> dict[str, Any]:
        return self.append(step, {'kind': 'observation', 'content': self.clean(content, OBSERVATION_LIMIT)})


def planner_evidence(state: dict[str, Any]) -> str:
    events = state.get('evidence', [])[-MAX_EVENTS:]
    return json.dumps(events, ensure_ascii=False) if events else '(no execution receipts yet)'
