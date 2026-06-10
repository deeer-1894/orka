"""Action planner.

Three modes, selected by GUI_PLANNER (uitars | vlm | rule):
  * uitars (recommended): a UI-TARS GUI-agent model (served on any
    OpenAI-compatible endpoint, e.g. vLLM) plans coordinate-grounded actions
    from raw screenshots — native grounding, no Set-of-Marks needed.
  * vlm: a generic multimodal model decides the next action from instruction +
    Set-of-Marks annotated screenshot.
  * rule (default): deterministic, DOM-first, no vision tokens. Enough to
    drive navigate/read/done flows and to run without a multimodal model.

Unset, GUI_PLANNER falls back to "vlm" when VLM_ENABLE=1, else "rule"
(preserving the pre-UI-TARS behavior).
"""

from __future__ import annotations

import asyncio
import os
from typing import Any

from operators import dom_first
from utils.parser import extract_url, parse_action
from utils.uitars import parse_uitars, png_size

# UI-TARS-1.5 mobile/web prompt, extended with navigate/navigate_back since a
# headless Playwright page has no browser chrome (no URL bar to click).
UITARS_PROMPT = """You are a GUI agent. You are given a task and your action history, with screenshots. You need to perform the next action to complete the task.

## Output Format
```
Thought: ...
Action: ...
```

## Action Space

click(start_box='<|box_start|>(x1,y1)<|box_end|>')
left_double(start_box='<|box_start|>(x1,y1)<|box_end|>')
right_single(start_box='<|box_start|>(x1,y1)<|box_end|>')
drag(start_box='<|box_start|>(x1,y1)<|box_end|>', end_box='<|box_start|>(x3,y3)<|box_end|>')
hotkey(key='')
type(content='') #If you want to submit your input, use "\\n" at the end of `content`.
scroll(start_box='<|box_start|>(x1,y1)<|box_end|>', direction='down or up or right or left')
navigate(content='') #Open the given URL directly in the browser.
navigate_back() #Go back to the previous page.
wait() #Sleep for 5s and take a screenshot to check for any changes.
finished(content='xxx') #Use escape characters \\', \\", and \\n in content part to ensure we can parse the content in normal python string format.
call_user() #Submit the task and call the user when the task is unsolvable, or when you need the user's help.

## Note
- Use the same language as the user instruction in the `Thought` part.
- When the task asks for information, put the answer itself in `finished(content=...)`.

## User Instruction
{instruction}
"""


def _image_part(b64: str) -> dict[str, Any]:
    return {"type": "image_url", "image_url": {"url": "data:image/png;base64," + b64}}


class UITarsPlanner:
    """Plans the next action with a UI-TARS model over a rolling multi-turn
    history of (screenshot, model response) pairs."""

    def __init__(self) -> None:
        self.base_url = os.getenv("UITARS_BASE_URL") or os.getenv("OPENAI_BASE_URL", "")
        self.api_key = os.getenv("UITARS_API_KEY") or os.getenv("OPENAI_API_KEY", "") or "empty"
        self.model = os.getenv("UITARS_MODEL", "ui-tars-1.5-7b")
        # screenshots kept in the conversation window (UI-TARS standard is ~5)
        self.max_shots = max(1, int(os.getenv("UITARS_MAX_SHOTS", "5")))

    def _messages(self, instruction: str, shots: list[str], responses: list[str]) -> list[dict[str, Any]]:
        # shots = all screenshots so far (last = current); responses align with
        # every shot except the latest. Truncate both to the same window; the
        # task prompt always rides on the oldest kept screenshot.
        shots = shots[-self.max_shots:]
        responses = responses[-(len(shots) - 1):] if len(shots) > 1 else []
        msgs: list[dict[str, Any]] = [{
            "role": "user",
            "content": [
                {"type": "text", "text": UITARS_PROMPT.format(instruction=instruction)},
                _image_part(shots[0]),
            ],
        }]
        for resp, shot in zip(responses, shots[1:]):
            msgs.append({"role": "assistant", "content": resp})
            msgs.append({"role": "user", "content": [_image_part(shot)]})
        return msgs

    def _chat(self, msgs: list[dict[str, Any]]) -> str:
        from openai import OpenAI

        client = OpenAI(base_url=self.base_url, api_key=self.api_key)
        resp = client.chat.completions.create(
            model=self.model, messages=msgs, max_tokens=400, temperature=0,
        )
        return resp.choices[0].message.content or ""

    async def predict(self, state: dict[str, Any]) -> dict[str, Any]:
        shots = state.get("shots") or ([state["screenshot"]] if state.get("screenshot") else [])
        if not shots:
            return {"action": "error", "message": "uitars planner: no screenshot"}
        msgs = self._messages(state.get("instruction", ""), shots, state.get("responses") or [])
        try:
            text = await asyncio.to_thread(self._chat, msgs)
        except Exception as e:  # noqa: BLE001
            return {"action": "error", "message": f"ui-tars model call failed: {e}"}
        w, h = png_size(shots[-1])
        action = parse_uitars(text, w, h)
        action["_raw"] = text  # graph appends this to the multi-turn history
        return action


class Planner:
    """Action planner. Modes:
    - rule   : zero-LLM heuristics (navigate + read only; cannot click)
    - llm    : text Set-of-Marks — the numbered element list goes to a TEXT
               model (MODEL env), no screenshot, so any chat model can click
    - vlm    : visual Set-of-Marks (screenshot + marks to a multimodal model)
    - uitars : UI-TARS coordinate planner
    """

    def __init__(self) -> None:
        mode = os.getenv("GUI_PLANNER", "").strip().lower()
        if not mode:
            mode = "vlm" if os.getenv("VLM_ENABLE") == "1" else "rule"
        self.mode = mode
        self.vlm_enabled = mode == "vlm"
        self.uitars = UITarsPlanner() if mode == "uitars" else None
        self.base_url = os.getenv("OPENAI_BASE_URL", "")
        self.api_key = os.getenv("OPENAI_API_KEY", "")
        if mode == "llm":
            self.model = os.getenv("MODEL", "gpt-4o-mini")  # text model suffices
        else:
            self.model = os.getenv("VLM_MODEL", "gpt-4o")

    async def predict(self, state: dict[str, Any], page) -> tuple[dict[str, Any], bool]:
        """Return (action, used_vision)."""
        if self.uitars is not None:
            return await self.uitars.predict(state), True

        # Set-of-Marks planning: "vlm" sends marks + screenshot to a multimodal
        # model; "llm" sends only the marks text to a cheap text model (the
        # screenshot node already numbered the interactive elements).
        if self.mode in ("vlm", "llm"):
            return await self._som_predict(state, page), self.vlm_enabled

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

    async def _som_predict(self, state: dict[str, Any], page=None) -> dict[str, Any]:
        try:
            from openai import OpenAI
        except Exception:
            return {"action": "error", "message": "openai sdk unavailable for planner"}
        client = OpenAI(base_url=self.base_url, api_key=self.api_key)
        sys = (
            "You are a GUI agent using Set-of-Marks. Interactive page elements are "
            "numbered. Output ONE JSON action object with key "
            "'action' in {navigate,click,type,scroll,read,done,call_user,error}. "
            "To click/type a tagged element, set 'mark' to its number (and 'text' "
            "for type). Check 'Current page' against the instruction: as soon as "
            "the goal is satisfied, output {\"action\":\"done\",\"result\":\"<short summary "
            "with final url/title>\"}. Never repeat an action that already succeeded. "
            "Output ONLY the JSON object, no explanation."
        )
        # Current page context lets the model recognise a click already landed.
        cur = ""
        if page is not None:
            try:
                cur = f"Current page: {page.url} — {await page.title()}"
            except Exception:
                cur = ""
        marks_text = state.get("marks_text", "")
        history = state.get("history", [])
        past = "\n".join(
            f"- {h.get('action', {}).get('action')} {h.get('action', {}).get('url') or h.get('action', {}).get('mark') or ''}"
            f" -> {str(h.get('result', ''))[:60]}"
            for h in history if h.get("action")
        )
        text = (
            f"Instruction: {state.get('instruction','')}\n\n{cur}\n\n"
            f"Actions so far:\n{past or '(none)'}\n\nElements:\n{marks_text}"
        )
        # Only the visual mode pays for image tokens; "llm" plans from text marks
        # and sends a plain string (text-only providers reject multipart content).
        content: Any = text
        if self.vlm_enabled and state.get("screenshot"):
            content = [
                {"type": "text", "text": text},
                {"type": "image_url", "image_url": {"url": "data:image/png;base64," + state["screenshot"]}},
            ]
        resp = client.chat.completions.create(
            model=self.model,
            messages=[{"role": "system", "content": sys}, {"role": "user", "content": content}],
            # Reasoning models (e.g. DeepSeek) may spend most of the budget on
            # hidden reasoning before emitting content — keep this generous.
            max_tokens=1500,
        )
        msg = resp.choices[0].message
        text_out = msg.content or getattr(msg, "reasoning_content", None) or ""
        return parse_action(text_out)
