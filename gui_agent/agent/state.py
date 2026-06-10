"""GraphState: the state threaded through the screenshot -> predict -> execute loop."""

from __future__ import annotations

from typing import Any, TypedDict


class GraphState(TypedDict, total=False):
    instruction: str          # natural-language task
    session_id: str
    screenshot: str           # base64 PNG of the current screen (when vision used)
    shots: list[str]          # rolling raw-screenshot window (uitars multi-turn)
    responses: list[str]      # raw model replies aligned to shots[:-1] (uitars)
    dom: str                  # textual a11y/DOM snapshot
    prediction: dict[str, Any]  # the next action chosen by predict
    history: list[dict[str, Any]]  # executed actions + results
    status: str               # "running" | "END" | "ERROR" | "CALL_USER"
    step: int
    max_steps: int
    result: str               # final summary on END
    error: str                # message on ERROR
    call_user: str            # reason on CALL_USER
    use_vision: bool          # True once DOM-first locate failed and we fell back to VLM
