"""Browser operator over Playwright.

Connects to a remote Chromium via CDP (connect_over_cdp) when CDP_URL is set,
otherwise launches a local headless Chromium. The action space is "MANUAL":
navigate / click / type / scroll / read. Vision (screenshots) is optional and
only used when DOM-first location fails.
"""

from __future__ import annotations

import base64
import os
from typing import Any

from playwright.async_api import Browser, Page, async_playwright


class RemoteBrowserOperator:
    ACTION_SPACE = ["navigate", "click", "type", "scroll", "read", "done"]

    def __init__(self, cdp_url: str | None = None, headless: bool = True):
        self.cdp_url = cdp_url or os.getenv("CDP_URL", "")
        self.headless = headless
        self._pw = None
        self._browser: Browser | None = None
        self._page: Page | None = None

    async def start(self) -> None:
        self._pw = await async_playwright().start()
        if self.cdp_url:
            self._browser = await self._pw.chromium.connect_over_cdp(self.cdp_url)
            ctx = self._browser.contexts[0] if self._browser.contexts else await self._browser.new_context()
            self._page = ctx.pages[0] if ctx.pages else await ctx.new_page()
        else:
            self._browser = await self._pw.chromium.launch(headless=self.headless)
            self._page = await self._browser.new_page()

    @property
    def page(self) -> Page:
        assert self._page is not None, "operator not started"
        return self._page

    async def screenshot(self) -> str:
        png = await self.page.screenshot(type="png")
        return base64.b64encode(png).decode("ascii")

    async def dom_snapshot(self, limit: int = 4000) -> str:
        """A compact textual snapshot: title + visible text (truncated)."""
        try:
            title = await self.page.title()
        except Exception:
            title = ""
        try:
            text = await self.page.inner_text("body")
        except Exception:
            text = ""
        text = " ".join(text.split())
        return f"TITLE: {title}\nTEXT: {text[:limit]}"

    async def execute(self, action: dict[str, Any]) -> str:
        kind = action.get("action")
        if kind == "navigate":
            url = action.get("url", "")
            await self.page.goto(url, wait_until="domcontentloaded")
            return f"navigated to {url}"
        if kind == "click":
            sel = action.get("selector") or action.get("target", "")
            await self.page.click(sel, timeout=5000)
            return f"clicked {sel}"
        if kind == "type":
            sel = action.get("selector") or action.get("target", "")
            text = action.get("text", "")
            await self.page.fill(sel, text, timeout=5000)
            return f"typed into {sel}"
        if kind == "scroll":
            dy = int(action.get("dy", 600))
            await self.page.mouse.wheel(0, dy)
            return f"scrolled {dy}"
        if kind == "read":
            return await self.dom_snapshot()
        return f"noop ({kind})"

    async def title(self) -> str:
        try:
            return await self.page.title()
        except Exception:
            return ""

    async def close(self) -> None:
        try:
            if self._browser:
                await self._browser.close()
        finally:
            if self._pw:
                await self._pw.stop()
