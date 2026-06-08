"""Action planner.

Two modes:
  * rule-based (default): deterministic, DOM-first, no vision tokens. Enough to
    drive navigate/read/done flows and to run without a multimodal model.
  * VLM (optional, VLM_ENABLE=1): an OpenAI-compatible multimodal model decides
    the next action from instruction + screenshot. Used as the fallback when
    DOM-first location fails.
"""

from __future__ import annotations

import os
from typing import Any

from operators import dom_first
from utils.parser import extract_url, parse_action


class Planner:
    def __init__(self) -> None:
        self.vlm_enabled = os.getenv("VLM_ENABLE") == "1"
        self.base_url = os.getenv("OPENAI_BASE_URL", "")
        self.api_key = os.getenv("OPENAI_API_KEY", "")
        self.model = os.getenv("VLM_MODEL", "gpt-4o")

    async def predict(self, state: dict[str, Any], page) -> tuple[dict[str, Any], bool]:
        """Return (action, used_vision)."""
        # Vision-first when a multimodal model is configured: drive via
        # Set-of-Marks (the screenshot node already numbered the elements).
        if self.vlm_enabled:
            return await self._vlm_predict(state), True

        # DOM-first rule planner (zero vision tokens).
        action = self._rule_predict(state)
        if action.get("action") in ("click", "type"):
            ok = await dom_first.locate(page, action)
            if not ok:
                return {"action": "error", "message": f"cannot locate target for {action}"}, False
        return action, False

    def _rule_predict(self, state: dict[str, Any]) -> dict[str, Any]:
        instruction = state.get("instruction", "")
        history = state.get("history", [])
        navigated = any(h.get("action", {}).get("action") == "navigate" for h in history)
        url = extract_url(instruction)
        if url and not navigated:
            return {"action": "navigate", "url": url}
        # nothing else structured to do -> finish with what we observed
        dom = state.get("dom", "")
        return {"action": "done", "result": dom[:400] if dom else "no content"}

    async def _vlm_predict(self, state: dict[str, Any]) -> dict[str, Any]:
        try:
            from openai import OpenAI
        except Exception:
            return {"action": "error", "message": "openai sdk unavailable for VLM"}
        client = OpenAI(base_url=self.base_url, api_key=self.api_key)
        sys = (
            "You are a GUI agent using Set-of-Marks. The screenshot has numbered "
            "tags on interactive elements. Output ONE JSON action object with key "
            "'action' in {navigate,click,type,scroll,read,done,call_user,error}. "
            "To click/type a tagged element, set 'mark' to its number (and 'text' "
            "for type). Otherwise use url/result. Output JSON only."
        )
        marks_text = state.get("marks_text", "")
        content = [
            {
                "type": "text",
                "text": f"Instruction: {state.get('instruction','')}\n\nElements:\n{marks_text}",
            },
        ]
        if state.get("screenshot"):
            content.append({
                "type": "image_url",
                "image_url": {"url": "data:image/png;base64," + state["screenshot"]},
            })
        resp = client.chat.completions.create(
            model=self.model,
            messages=[{"role": "system", "content": sys}, {"role": "user", "content": content}],
            max_tokens=300,
        )
        return parse_action(resp.choices[0].message.content or "")
