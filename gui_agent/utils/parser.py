"""Parse a model's free-form output into a structured action dict."""

from __future__ import annotations

import json
import re
from typing import Any

_VALID = {"navigate", "click", "type", "scroll", "read", "done", "call_user", "error"}


def parse_action(text: str) -> dict[str, Any]:
    """Extract an action JSON object from the model output.

    Accepts a bare JSON object or one embedded in ```json fences. Falls back to
    an error action when nothing parseable is found.
    """
    if not text:
        return {"action": "error", "message": "empty model output"}

    candidate = text.strip()
    fence = re.search(r"```(?:json)?\s*(\{.*?\})\s*```", candidate, re.DOTALL)
    if fence:
        candidate = fence.group(1)
    else:
        brace = re.search(r"\{.*\}", candidate, re.DOTALL)
        if brace:
            candidate = brace.group(0)

    try:
        obj = json.loads(candidate)
    except json.JSONDecodeError:
        return {"action": "error", "message": f"unparseable: {text[:120]}"}

    if obj.get("action") not in _VALID:
        return {"action": "error", "message": f"invalid action: {obj.get('action')}"}
    return obj


_URL_RE = re.compile(r"https?://[^\s'\"]+")


def extract_url(text: str) -> str:
    m = _URL_RE.search(text or "")
    return m.group(0) if m else ""
