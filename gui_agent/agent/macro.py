"""Action-macro store: record a successful task's action sequence and replay it
deterministically on the same instruction, skipping predict/VLM entirely.

This is the GUI cost-reduction lever for repeated tasks ("固化"): the first run
explores (rule planner / VLM); subsequent identical instructions replay the
recorded macro at zero predict cost. If replay fails the caller falls back to the
normal explore loop.
"""

from __future__ import annotations

import json
import os
import threading
from typing import Any


class MacroStore:
    def __init__(self, path: str):
        self.path = path
        self._lock = threading.Lock()
        self._data: dict[str, list[dict[str, Any]]] = {}
        self._load()

    @staticmethod
    def key(instruction: str) -> str:
        return " ".join((instruction or "").lower().split())

    def _load(self) -> None:
        try:
            with open(self.path, encoding="utf-8") as f:
                self._data = json.load(f)
        except (OSError, json.JSONDecodeError):
            self._data = {}

    def _save(self) -> None:
        os.makedirs(os.path.dirname(self.path) or ".", exist_ok=True)
        tmp = self.path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(self._data, f)
        os.replace(tmp, self.path)

    def get(self, instruction: str) -> list[dict[str, Any]] | None:
        with self._lock:
            macro = self._data.get(self.key(instruction))
            return list(macro) if macro else None

    def put(self, instruction: str, actions: list[dict[str, Any]]) -> None:
        if not actions:
            return
        with self._lock:
            self._data[self.key(instruction)] = actions
            self._save()
