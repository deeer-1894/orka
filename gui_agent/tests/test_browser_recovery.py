"""Failures at the actual channel/lease boundary; no provider or user sessions."""
import asyncio
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock

from service.browser_bridge import PageChannel, BridgeError
from service.browser_runtime import BrowserRuntime


class Page:
    def __init__(self):
        self.close = AsyncMock()
        self.main_frame = object()

    def on(self, *args):
        pass


class RecoveryTests(unittest.IsolatedAsyncioTestCase):
    def runtime(self):
        page = Page()
        runtime = BrowserRuntime(SimpleNamespace(get=AsyncMock(return_value=(SimpleNamespace(page=page), True))))
        return page, runtime, {"owner_id": "test", "conversation_id": "recovery", "run_id": "run"}

    async def test_failed_read_keeps_page_and_next_lease_context(self):
        page = Page()
        operator = SimpleNamespace(page=page)
        runtime = BrowserRuntime(SimpleNamespace(get=AsyncMock(return_value=(operator, True))))
        who = {"owner_id": "test", "conversation_id": "recovery", "run_id": "run"}
        async with runtime.lease(who, "read", 1, 1) as lease:
            lease.state.cdp = SimpleNamespace(send=AsyncMock(side_effect=RuntimeError("read failed")))
            before = lease.state.page_id
            with self.assertRaises(BridgeError):
                await PageChannel(lease).execute({"id": 1, "method": "Page.getFrameTree"})
        page.close.assert_not_awaited()
        async with runtime.lease(who, "next", 1, 1) as lease:
            self.assertEqual(lease.state.page_id, before)

    async def test_unconfirmed_mutation_cannot_be_erased_by_a_later_read(self):
        page, runtime, who = self.runtime()
        async with runtime.lease(who, "input", 1, 1) as lease:
            lease.state.cdp = SimpleNamespace(send=AsyncMock(side_effect=RuntimeError("input delivery unknown")))
            channel = PageChannel(lease)
            with self.assertRaises(BridgeError) as caught:
                await channel.execute({"id": 1, "method": "Input.insertText", "params": {"text": "once"}})
            self.assertEqual(caught.exception.code, "outcome_unknown")
            with self.assertRaises(BridgeError):
                await channel.execute({"id": 2, "method": "Page.getFrameTree"})
            self.assertEqual(lease.state.cdp.send.await_count, 1)
        page.close.assert_awaited_once()

    async def test_cancelled_read_does_not_close_page(self):
        page, runtime, who = self.runtime()
        entered = asyncio.Event()
        async def read(*args):
            entered.set()
            await asyncio.Event().wait()
        async def run():
            async with runtime.lease(who, "read", 1, 2) as lease:
                lease.state.cdp = SimpleNamespace(send=read)
                await PageChannel(lease).execute({"id": 1, "method": "Page.getFrameTree"})
        task = asyncio.create_task(run())
        await entered.wait()
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        page.close.assert_not_awaited()

    async def test_failed_stop_or_unsettled_navigation_closes_and_quarantines_if_close_fails(self):
        for failure in ("stop", "pending", "close"):
            with self.subTest(failure=failure):
                page, runtime, who = self.runtime()
                if failure == "close":
                    page.close.side_effect = RuntimeError("cannot confirm closure")
                async with runtime.lease(who, "navigation", 1, 2) as lease:
                    async def send(method, params=None):
                        if method == "Page.stopLoading" and failure != "pending":
                            raise RuntimeError("stop not acknowledged")
                        return {}
                    lease.state.cdp = SimpleNamespace(send=send)
                    lease.navigation = asyncio.create_task(asyncio.Event().wait())
                    lease.uncertain = True
                page.close.assert_awaited_once()
                self.assertEqual(runtime._quarantined, failure == "close")
                if failure == "close":
                    with self.assertRaisesRegex(RuntimeError, "quarantined"):
                        async with runtime.lease(who, "blocked", 1, 1):
                            self.fail("unconfirmed cleanup released a usable lease")

    async def test_navigation_stop_and_settlement_hold_queue_despite_repeated_cancel(self):
        page, runtime, who = self.runtime()
        entered, stopping, settle = asyncio.Event(), asyncio.Event(), asyncio.Event()
        async def send(method, params=None):
            if method == "Page.getFrameTree":
                return {"frameTree": {"frame": {"id": "main"}}}
            if method == "Page.navigate":
                entered.set()
                await settle.wait()
                return {"errorText": "net::ERR_ABORTED"}
            if method == "Page.stopLoading":
                stopping.set()
            return {}
        async def run():
            async with runtime.lease(who, "navigation", 1, 2) as lease:
                lease.state.cdp = SimpleNamespace(send=send)
                await PageChannel(lease).execute({"id": 1, "method": "Page.navigate", "params": {"url": "https://fixture.test/"}})
        next_entered = asyncio.Event()
        async def next_lease():
            async with runtime.lease(who, "next", 1, 1):
                next_entered.set()
        task = asyncio.create_task(run())
        await entered.wait()
        task.cancel()
        await stopping.wait()
        waiter = asyncio.create_task(next_lease())
        task.cancel()
        task.cancel()
        await asyncio.sleep(.02)
        self.assertFalse(next_entered.is_set())
        settle.set()
        with self.assertRaises(asyncio.CancelledError):
            await task
        await waiter
        page.close.assert_not_awaited()
    async def test_object_release_failure_does_not_close_page(self):
        page = Page()
        runtime = BrowserRuntime(SimpleNamespace(get=AsyncMock(return_value=(SimpleNamespace(page=page), True))))
        who = {"owner_id": "test", "conversation_id": "recovery", "run_id": "run"}
        async with runtime.lease(who, "read", 1, 1) as lease:
            lease.state.groups.add("observation")
            lease.state.cdp = SimpleNamespace(send=AsyncMock(side_effect=RuntimeError("release failed")), detach=AsyncMock())
        page.close.assert_not_awaited()
