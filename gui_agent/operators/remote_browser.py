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

    def __init__(self, cdp_url: str | None = None, headless: bool | None = None):
        self.cdp_url = cdp_url or os.getenv("CDP_URL", "")
        if headless is None:
            headless = os.getenv("HEADLESS", "1") != "0"
        self.headless = headless
        self._pw = None
        self._browser: Browser | None = None
        self._context = None  # set in non-CDP (persistent) mode
        self._page: Page | None = None

    async def start(self) -> None:
        self._pw = await async_playwright().start()
        if self.cdp_url:
            self._browser = await self._pw.chromium.connect_over_cdp(self.cdp_url)
            # Attach to the page that's actually displayed in noVNC: scan every
            # context for a pre-existing page rather than blindly taking
            # contexts[0] (which can be an ephemeral context we don't see).
            page = None
            # prefer a real, already-open page (the visible one) over the blank
            # page Playwright spins up on connect.
            for c in self._browser.contexts:
                for pg in c.pages:
                    if pg.url and pg.url != "about:blank":
                        page = pg
                        break
                if page:
                    break
            if page is None:
                for c in self._browser.contexts:
                    if c.pages:
                        page = c.pages[0]
                        break
            if page is None:
                ctx = self._browser.contexts[0] if self._browser.contexts else await self._browser.new_context()
                page = await ctx.new_page()
            self._page = page
            try:
                await self._page.bring_to_front()
            except Exception:
                pass
        else:
            # launch_persistent_context gives Playwright control of Chrome's real
            # foreground window (pages[0] == the visible window), so the noVNC view
            # mirrors exactly what the agent does — unlike launch(), whose page
            # lands in a separate background tab.
            self._context = await self._pw.chromium.launch_persistent_context(
                user_data_dir="/tmp/orka-profile",
                headless=self.headless,
                no_viewport=True,
                args=["--no-sandbox", "--disable-dev-shm-usage", "--start-maximized"],
            )
            self._page = self._context.pages[0] if self._context.pages else await self._context.new_page()
            try:
                await self._page.bring_to_front()
            except Exception:
                pass

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
        # keep the live view on the tab the agent is acting in
        try:
            await self.page.bring_to_front()
        except Exception:
            pass
        # set-of-marks: act directly on a resolved element handle
        handle = action.get("_handle")
        if handle is not None and kind in ("click", "type"):
            if kind == "click":
                await handle.click(timeout=5000)
                return f"clicked mark {action.get('target', '')}"
            await handle.fill(action.get("text", ""), timeout=5000)
            return f"typed into mark {action.get('target', '')}"
        if kind == "navigate":
            url = action.get("url", "")
            await self.page.goto(url, wait_until="load", timeout=30000)
            # give SPA content a moment to paint before the next screenshot
            try:
                await self.page.wait_for_load_state("networkidle", timeout=5000)
            except Exception:
                pass
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
            # CDP mode: only disconnect (the container owns the browser).
            # Local mode: close the persistent context we launched.
            if not self.cdp_url and self._context:
                await self._context.close()
        finally:
            if self._pw:
                await self._pw.stop()
