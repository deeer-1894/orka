"""Set-of-Marks: overlay numbered tags on interactive elements so a VLM can
refer to "element N" instead of guessing pixel coordinates. Improves accuracy
and cuts retries on the vision fallback path.
"""

from __future__ import annotations

import base64
import io
from dataclasses import dataclass
from typing import Any

from PIL import Image, ImageDraw, ImageFont
from playwright.async_api import ElementHandle, Page

_SELECTOR = "a, button, input, textarea, select, [role=button], [role=link], [role=tab], [onclick]"


@dataclass
class Mark:
    index: int
    role: str
    name: str
    box: dict[str, float]  # x, y, width, height (viewport CSS px)
    handle: ElementHandle


async def collect_marks(page: Page, limit: int = 40) -> list[Mark]:
    """Collect visible interactive elements with their bounding boxes."""
    handles = await page.query_selector_all(_SELECTOR)
    marks: list[Mark] = []
    for h in handles:
        if len(marks) >= limit:
            break
        try:
            if not await h.is_visible():
                continue
            box = await h.bounding_box()
            if not box or box["width"] < 6 or box["height"] < 6:
                continue
            role = (await h.get_attribute("role")) or (await h.evaluate("e => e.tagName.toLowerCase()"))
            name = ((await h.inner_text()) or "").strip()
            if not name:
                name = (await h.get_attribute("aria-label")) or (await h.get_attribute("value")) or ""
            marks.append(
                Mark(index=len(marks), role=role, name=name[:40].replace("\n", " "), box=box, handle=h)
            )
        except Exception:
            continue
    return marks


def annotate(png_bytes: bytes, marks: list[Mark]) -> bytes:
    """Draw numbered boxes over the screenshot for each mark."""
    img = Image.open(io.BytesIO(png_bytes)).convert("RGB")
    draw = ImageDraw.Draw(img)
    try:
        font = ImageFont.truetype("Arial.ttf", 13)
    except Exception:
        font = ImageFont.load_default()
    for m in marks:
        b = m.box
        x0, y0, x1, y1 = b["x"], b["y"], b["x"] + b["width"], b["y"] + b["height"]
        draw.rectangle([x0, y0, x1, y1], outline=(79, 214, 224), width=2)
        label = str(m.index)
        tw = draw.textlength(label, font=font)
        draw.rectangle([x0, y0 - 16, x0 + tw + 8, y0], fill=(79, 214, 224))
        draw.text((x0 + 4, y0 - 15), label, fill=(10, 12, 16), font=font)
    out = io.BytesIO()
    img.save(out, format="PNG")
    return out.getvalue()


def marks_to_b64_annotated(png_bytes: bytes, marks: list[Mark]) -> str:
    return base64.b64encode(annotate(png_bytes, marks)).decode("ascii")


def marks_text(marks: list[Mark]) -> str:
    """A compact textual index for the VLM prompt."""
    lines = [f"[{m.index}] {m.role}: {m.name or '(no label)'}" for m in marks]
    return "\n".join(lines)


def mark_action_to_dict(action: dict[str, Any], marks: list[Mark]) -> dict[str, Any]:
    """Resolve a VLM action that references a mark index into a concrete action
    carrying the target ElementHandle under '_handle'."""
    idx = action.get("mark")
    if idx is None:
        return action
    try:
        m = marks[int(idx)]
    except (ValueError, IndexError, TypeError):
        return {"action": "error", "message": f"invalid mark {idx}"}
    resolved = dict(action)
    resolved["_handle"] = m.handle
    resolved["target"] = f"[{m.index}] {m.name}"
    return resolved
