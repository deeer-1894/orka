"""Browser actions on a page leased from the isolated session pool.

Browser/context lifecycle belongs to operators.sessions. This operator never
attaches to a default CDP context or reads/writes a shared browser profile.
"""

from __future__ import annotations

import asyncio
import base64
import re
from typing import Any

from playwright.async_api import Page

from operators.privacy import SENSITIVE_ELEMENT

# human key names (UI-TARS hotkey style: "ctrl c") -> Playwright key names
_KEYMAP = {
    "ctrl": "Control", "control": "Control", "shift": "Shift", "alt": "Alt",
    "cmd": "Meta", "meta": "Meta", "win": "Meta",
    "enter": "Enter", "return": "Enter", "esc": "Escape", "escape": "Escape",
    "tab": "Tab", "space": "Space", "backspace": "Backspace", "delete": "Delete",
    "up": "ArrowUp", "down": "ArrowDown", "left": "ArrowLeft", "right": "ArrowRight",
    "pageup": "PageUp", "pagedown": "PageDown", "home": "Home", "end": "End",
}


def hotkey_to_playwright(s: str) -> str:
    parts = [p for p in re.split(r"[\s+]+", (s or "").strip()) if p]
    return "+".join(_KEYMAP.get(p.lower(), p) for p in parts)


class RemoteBrowserOperator:
    ACTION_SPACE = [
        "navigate", "navigate_back", "click", "drag", "type", "hotkey",
        "scroll", "wait", "read", "done",
    ]

    def __init__(self, page: Page | None = None):
        self._page = page

    @property
    def page(self) -> Page:
        assert self._page is not None, "operator not started"
        return self._page

    async def screenshot(self) -> str:
        png = await self.page.screenshot(type="png")
        return base64.b64encode(png).decode("ascii")

    async def dom_snapshot(self, limit: int = 4000) -> str:
        """A compact textual snapshot: title + main content (truncated).

        Prefers a main/results content region over raw body text so the snippet
        surfaces the actual content (e.g. result titles) instead of leading nav
        chrome (region pickers, menus). Also lists the top link texts, which is
        what "first result"-style questions usually need.
        """
        try:
            title = await self.page.title()
        except Exception:
            title = ""
        text = ""
        for sel in ("main", "[role=main]", "#links", ".results", "#content", "article", "body"):
            try:
                if await self.page.query_selector(sel):
                    text = await self.page.inner_text(sel)
                    if text.strip():
                        break
            except Exception:
                continue
        text = " ".join(text.split())
        links = []
        try:
            links = await self.page.eval_on_selector_all(
                "a", "els => els.map(e => (e.innerText||'').trim()).filter(t => t.length > 8).slice(0, 8)"
            )
        except Exception:
            pass
        out = f"TITLE: {title}\nTEXT: {text[:limit]}"
        if links:
            out += "\nTOP LINKS: " + " | ".join(links)
        return out

    async def input_is_sensitive(self, action: dict[str, Any]) -> bool:
        try:
            handle = action.get("_handle")
            if handle is not None:
                return bool(await handle.evaluate(SENSITIVE_ELEMENT))
            selector = action.get("selector") or action.get("target")
            if selector:
                return bool(await self.page.locator(selector).evaluate(SENSITIVE_ELEMENT))
            return bool(await self.page.evaluate(
                "() => (" + SENSITIVE_ELEMENT + ")(document.activeElement)"
            ))
        except Exception:
            return True

    async def execute(self, action: dict[str, Any]) -> str:
        kind = action.get("action")
        # keep the live view on the tab the agent is acting in
        try:
            await self.page.bring_to_front()
        except Exception:
            pass
        # set-of-marks: act directly on a resolved element handle
        handle = action.get("_handle")
        if handle is not None and kind in ("click", "type"):
            if kind == "click":
                await handle.click(timeout=2500)
                return f"clicked mark {action.get('target', '')}"
            await handle.fill(action.get("text", ""), timeout=2500)
            return f"typed into mark {action.get('target', '')}"
        if kind == "navigate":
            url = action.get("url", "")
            await self.page.goto(url, wait_until="load", timeout=30000)
            # give SPA content a moment to paint before the next screenshot
            try:
                await self.page.wait_for_load_state("networkidle", timeout=2500)
            except Exception:
                pass
            return f"navigated to {url}"
        if kind == "navigate_back":
            await self.page.go_back(wait_until="load", timeout=30000)
            return "navigated back"
        # coordinate-grounded actions (UI-TARS): act at a viewport point
        if kind == "click" and "x" in action:
            x, y = float(action["x"]), float(action["y"])
            await self.page.mouse.click(
                x, y,
                button=action.get("button", "left"),
                click_count=int(action.get("count", 1)),
            )
            return f"clicked ({x:.0f},{y:.0f})"
        if kind == "drag":
            x, y = float(action["x"]), float(action["y"])
            x2, y2 = float(action["x2"]), float(action["y2"])
            await self.page.mouse.move(x, y)
            await self.page.mouse.down()
            await self.page.mouse.move(x2, y2, steps=12)
            await self.page.mouse.up()
            return f"dragged ({x:.0f},{y:.0f}) -> ({x2:.0f},{y2:.0f})"
        if kind == "click":
            sel = action.get("selector") or action.get("target", "")
            await self.page.click(sel, timeout=2500)
            return f"clicked {sel}"
        if kind == "type":
            sel = action.get("selector") or action.get("target", "")
            text = action.get("text", "")
            if not sel:
                # coordinate mode: type into the focused element; a trailing
                # newline means "submit" (press Enter), per the UI-TARS contract.
                submit = text.endswith("\n")
                await self.page.keyboard.type(text.rstrip("\n"), delay=20)
                if submit:
                    await self.page.keyboard.press("Enter")
                return f"typed {len(text)} chars" + (" + Enter" if submit else "")
            await self.page.fill(sel, text, timeout=2500)
            return f"typed into {sel}"
        if kind == "hotkey":
            combo = hotkey_to_playwright(action.get("key", ""))
            if not combo:
                return "noop (empty hotkey)"
            await self.page.keyboard.press(combo)
            return f"pressed {combo}"
        if kind == "scroll":
            direction = action.get("direction", "")
            if "x" in action:
                await self.page.mouse.move(float(action["x"]), float(action["y"]))
            if direction:
                step = 600
                dx, dy = {
                    "up": (0, -step), "down": (0, step),
                    "left": (-step, 0), "right": (step, 0),
                }.get(direction, (0, step))
            else:
                dx, dy = 0, int(action.get("dy", 600))
            await self.page.mouse.wheel(dx, dy)
            return f"scrolled {direction or dy}"
        if kind == "wait":
            await asyncio.sleep(5)
            return "waited 5s"
        if kind == "read":
            return await self.dom_snapshot()
        return f"noop ({kind})"

    async def title(self) -> str:
        try:
            return await self.page.title()
        except Exception:
            return ""
