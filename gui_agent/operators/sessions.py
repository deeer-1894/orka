"""Owner/conversation browser contexts, owned in memory by the GUI service.

The execution queue serializes visible interaction. This pool owns only contexts
it creates; it never attaches to an existing user's default CDP context/profile.
"""
import asyncio
import os
import time
from collections import OrderedDict
from urllib.parse import unquote, urlsplit, urlunsplit

from playwright.async_api import async_playwright
from operators.remote_browser import RemoteBrowserOperator


def _container_proxy_host(host):
    # Compose passes the host proxy into an isolated container. A loopback
    # address would otherwise point back at the container itself.
    if host in ("localhost", "127.0.0.1", "::1"):
        return "host.docker.internal"
    return host


def browser_proxy_from_env(environ=None):
    """Build Playwright's proxy option without exposing credentials to logs."""
    environ = os.environ if environ is None else environ
    raw = next((environ.get(name, "").strip() for name in (
        "BROWSER_PROXY_SERVER", "HTTPS_PROXY", "https_proxy",
        "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy",
    ) if environ.get(name, "").strip()), "")
    if not raw:
        return None
    parsed = urlsplit(raw if "://" in raw else "http://" + raw)
    if parsed.scheme not in ("http", "https", "socks4", "socks5") or not parsed.hostname:
        raise ValueError("browser proxy must be an HTTP(S) or SOCKS URL")
    host = _container_proxy_host(parsed.hostname)
    if ":" in host and not host.startswith("["):
        host = "[" + host + "]"
    authority = host + ((":" + str(parsed.port)) if parsed.port else "")
    option = {"server": urlunsplit((parsed.scheme, authority, "", "", ""))}
    if parsed.username is not None:
        option["username"] = unquote(parsed.username)
    if parsed.password is not None:
        option["password"] = unquote(parsed.password)
    bypass = environ.get("BROWSER_PROXY_BYPASS", "").strip()
    if not bypass:
        bypass = environ.get("NO_PROXY", environ.get("no_proxy", "")).strip()
    if bypass:
        option["bypass"] = bypass
    return option


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
                options = {
                    "headless": self.headless,
                    "args": ["--no-sandbox", "--disable-dev-shm-usage", "--start-maximized"],
                }
                proxy = browser_proxy_from_env()
                if proxy:
                    options["proxy"] = proxy
                self.browser = await self._pw.chromium.launch(**options)
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
