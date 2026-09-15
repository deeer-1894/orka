"""Owner/conversation browser contexts, owned in memory by the GUI service.

The execution queue serializes visible interaction. This pool owns only contexts
it creates; it never attaches to an existing user's default CDP context/profile.
"""
import asyncio
import os
import time
from collections import OrderedDict

from playwright.async_api import async_playwright
from operators.remote_browser import RemoteBrowserOperator


class SessionPool:
    def __init__(self, browser=None, *, max_sessions=16, idle_ttl=1800, headless=None, cdp_url=""):
        self.browser = browser
        self.max_sessions = max(1, max_sessions)
        self.idle_ttl = idle_ttl
        self.headless = os.getenv("HEADLESS", "1") != "0" if headless is None else headless
        self.cdp_url = cdp_url
        self._sessions = OrderedDict()
        self._lock = asyncio.Lock()
        self._pw = None

    async def start(self):
        if self.browser is not None:
            return
        self._pw = await async_playwright().start()
        try:
            if self.cdp_url:
                self.browser = await self._pw.chromium.connect_over_cdp(self.cdp_url)
            else:
                self.browser = await self._pw.chromium.launch(headless=self.headless,
                    args=["--no-sandbox", "--disable-dev-shm-usage", "--start-maximized"])
        except BaseException:
            await self._pw.stop()
            self._pw = None
            raise

    async def get(self, owner_id, conversation_id):
        if not owner_id or not conversation_id:
            raise ValueError("trusted owner and conversation are required")
        async with self._lock:
            await self.start()
            now = time.monotonic()
            for key, (_, context, touched) in list(self._sessions.items()):
                if now - touched > self.idle_ttl:
                    await context.close()
                    del self._sessions[key]
            key = (owner_id, conversation_id)
            existing = self._sessions.get(key)
            resumed = existing is not None
            if existing:
                operator, context, _ = existing
                if operator.page.is_closed():
                    operator = RemoteBrowserOperator(await context.new_page())
            else:
                while len(self._sessions) >= self.max_sessions:
                    _, (_, old_context, _) = self._sessions.popitem(last=False)
                    await old_context.close()
                context = await self.browser.new_context(no_viewport=True)
                try:
                    operator = RemoteBrowserOperator(await context.new_page())
                except BaseException:
                    await context.close()
                    raise
            self._sessions[key] = (operator, context, now)
            self._sessions.move_to_end(key)
            await operator.page.bring_to_front()
            return operator, resumed

    async def close(self):
        async with self._lock:
            for _, context, _ in self._sessions.values():
                await context.close()
            self._sessions.clear()
            if self._pw:
                if self.browser and not self.cdp_url:
                    await self.browser.close()
                await self._pw.stop()
                self.browser = None
                self._pw = None
