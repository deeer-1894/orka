"""UI-TARS output parsing + coordinate remapping.

UI-TARS replies in a `Thought: ...\nAction: <call>` format where <call> is a
small DSL, e.g. click(start_box='<|box_start|>(x,y)<|box_end|>'). Coordinates
are in the qwen2.5-vl smart-resize space of the screenshot the model saw;
parse_uitars() projects them back onto the original screenshot dimensions
before Playwright executes them.
"""

from __future__ import annotations

import base64
import math
import re
import struct
from typing import Any

# qwen2.5-vl preprocessing constants (UI-TARS-1.5 defaults).
FACTOR = 28
MIN_PIXELS = 100 * 28 * 28
MAX_PIXELS = 16384 * 28 * 28


def smart_resize(
    height: int,
    width: int,
    factor: int = FACTOR,
    min_pixels: int = MIN_PIXELS,
    max_pixels: int = MAX_PIXELS,
) -> tuple[int, int]:
    """The (height, width) the vision encoder actually saw: dimensions rounded
    to multiples of `factor` with total area scaled into [min_pixels, max_pixels].
    Mirrors qwen_vl_utils.smart_resize so model coordinates can be inverted."""
    h_bar = max(factor, round(height / factor) * factor)
    w_bar = max(factor, round(width / factor) * factor)
    if h_bar * w_bar > max_pixels:
        beta = math.sqrt((height * width) / max_pixels)
        h_bar = math.floor(height / beta / factor) * factor
        w_bar = math.floor(width / beta / factor) * factor
    elif h_bar * w_bar < min_pixels:
        beta = math.sqrt(min_pixels / (height * width))
        h_bar = math.ceil(height * beta / factor) * factor
        w_bar = math.ceil(width * beta / factor) * factor
    return h_bar, w_bar


def png_size(b64: str) -> tuple[int, int]:
    """(width, height) from a base64 PNG's IHDR header (no full decode)."""
    raw = base64.b64decode(b64[:64])
    if len(raw) < 24 or raw[:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError("not a PNG")
    w, h = struct.unpack(">II", raw[16:24])
    return int(w), int(h)


_NUM_RE = re.compile(r"-?\d+(?:\.\d+)?")
_CALL_RE = re.compile(r"([A-Za-z_]\w*)\s*\((.*)\)", re.DOTALL)
_PARAM_RE = re.compile(
    r"(\w+)\s*=\s*(?:'((?:\\.|[^'\\])*)'|\"((?:\\.|[^\"\\])*)\")", re.DOTALL
)
_ESCAPES = {"n": "\n", "t": "\t"}


def _unescape(s: str) -> str:
    return re.sub(r"\\(.)", lambda m: _ESCAPES.get(m.group(1), m.group(1)), s)


def _coords(val: str) -> tuple[float, float] | None:
    """Extract a point from a box/point param value. Accepts '(x,y)',
    '<|box_start|>(x,y)<|box_end|>', '<point>x y</point>' and 4-number boxes
    (returns the center)."""
    nums = [float(x) for x in _NUM_RE.findall(val)]
    if len(nums) >= 4:
        return (nums[0] + nums[2]) / 2, (nums[1] + nums[3]) / 2
    if len(nums) >= 2:
        return nums[0], nums[1]
    return None


def _split_thought_action(text: str) -> tuple[str, str]:
    thought = ""
    m = re.search(r"Thought\s*:\s*(.*?)(?:\n\s*Action\s*:|$)", text, re.DOTALL)
    if m:
        thought = m.group(1).strip()
    am = re.search(r"Action\s*:\s*(.*)", text, re.DOTALL)
    body = (am.group(1) if am else text).strip()
    return thought, body


def parse_uitars(text: str, orig_w: int, orig_h: int) -> dict[str, Any]:
    """Parse a UI-TARS response into the internal action dict, remapping
    model-space coordinates onto the orig_w x orig_h screenshot. The raw
    thought is carried as `_thought` for observability."""
    if not text or not text.strip():
        return {"action": "error", "message": "empty model output"}
    thought, body = _split_thought_action(text)

    call = _CALL_RE.search(body)
    if not call:
        return {"action": "error", "message": f"no action call in: {body[:120]}"}
    name = call.group(1).lower()
    params: dict[str, str] = {}
    for m in _PARAM_RE.finditer(call.group(2)):
        params[m.group(1)] = _unescape(m.group(2) if m.group(2) is not None else m.group(3) or "")

    # invert smart_resize: model coords -> original screenshot coords
    rh, rw = smart_resize(orig_h, orig_w)

    def point(key: str) -> tuple[float, float] | None:
        v = params.get(key)
        if v is None:
            return None
        c = _coords(v)
        if c is None:
            return None
        x = min(max(c[0] / rw * orig_w, 0), orig_w - 1)
        y = min(max(c[1] / rh * orig_h, 0), orig_h - 1)
        return x, y

    out: dict[str, Any] = {"_thought": thought}

    if name in ("click", "left_single", "left_double", "right_single"):
        p = point("start_box") or point("point")
        if p is None:
            return {"action": "error", "message": f"{name} without coordinates"}
        out.update({"action": "click", "x": p[0], "y": p[1]})
        if name == "left_double":
            out["count"] = 2
        if name == "right_single":
            out["button"] = "right"
    elif name == "drag":
        p1 = point("start_box")
        p2 = point("end_box")
        if p1 is None or p2 is None:
            return {"action": "error", "message": "drag without start/end coordinates"}
        out.update({"action": "drag", "x": p1[0], "y": p1[1], "x2": p2[0], "y2": p2[1]})
    elif name == "type":
        out.update({"action": "type", "text": params.get("content", "")})
    elif name == "hotkey":
        out.update({"action": "hotkey", "key": params.get("key", "")})
    elif name == "scroll":
        p = point("start_box") or point("point")
        out.update({"action": "scroll", "direction": params.get("direction", "down")})
        if p is not None:
            out.update({"x": p[0], "y": p[1]})
    elif name in ("navigate", "open_url", "goto"):
        out.update({"action": "navigate", "url": params.get("content") or params.get("url", "")})
    elif name in ("navigate_back", "back"):
        out.update({"action": "navigate_back"})
    elif name == "wait":
        out.update({"action": "wait"})
    elif name == "finished":
        out.update({"action": "done", "result": params.get("content") or thought or "done"})
    elif name == "call_user":
        out.update({"action": "call_user", "reason": params.get("content") or thought or "user input required"})
    else:
        return {"action": "error", "message": f"unknown ui-tars action: {name}"}
    return out
