"""Browser runtime contracts against isolated, provider-free Chromium."""
import asyncio
import unittest
from playwright.async_api import async_playwright
from operators.sessions import SessionPool


class BrowserRuntimeTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        from service.browser_runtime import BrowserRuntime
        self.pw = await async_playwright().start()
        self.browser = await self.pw.chromium.launch(args=["--no-sandbox"])
        self.pool = SessionPool(browser=self.browser)
        self.runtime = BrowserRuntime(self.pool)
        self.identity = {"owner_id":"alice", "conversation_id":"one", "run_id":"r1"}

    async def asyncTearDown(self):
        if hasattr(self, "pool"):
            await self.pool.close()
            await self.browser.close()
            await self.pw.stop()

    async def test_gui_and_browser_share_page_and_never_overlap(self):
        entered = asyncio.Event()
        async def browser_call():
            async with self.runtime.lease(self.identity, "browser", 2, 2, "browser") as lease:
                entered.set()
                self.assertIs(lease.operator.page, gui_page)
                self.assertTrue(lease.resumed)
                self.assertEqual(await lease.operator.page.input_value("input"), "completed GUI input")
        async with self.runtime.lease(self.identity, "gui", 2, 2, "gui") as lease:
            gui_page = lease.operator.page
            await gui_page.set_content("<input>")
            await gui_page.fill("input", "completed GUI input")
            waiter = asyncio.create_task(browser_call())
            await asyncio.sleep(.02)
            self.assertFalse(entered.is_set())
        await waiter
        self.assertTrue(entered.is_set())

    async def test_epoch_navigation_gui_run_and_reconstruction(self):
        async with self.runtime.lease(self.identity, "first", 1, 2) as first:
            info = first.scope()
            await first.operator.page.route("http://fake.test/**", lambda route: route.fulfill(body="<p>fake</p>"))
            await first.operator.page.goto("http://fake.test/")
            self.assertGreater(first.state.epoch, info["page_epoch"])
            epoch = first.state.epoch
        async with self.runtime.lease(self.identity, "second", 1, 2) as second:
            self.assertEqual(second.state.epoch, epoch)
        async with self.runtime.lease(self.identity, "gui", 1, 2, "gui") as gui:
            self.assertGreater(gui.state.epoch, epoch)
            epoch = gui.state.epoch
        async with self.runtime.lease({**self.identity,"run_id":"r2"}, "run", 1, 2) as newrun:
            self.assertGreater(newrun.state.epoch, epoch)
            await newrun.operator.page.close()
        async with self.runtime.lease(self.identity, "newpage", 1, 2) as rebuilt:
            self.assertNotEqual(rebuilt.state.page_id, info["page_id"])

    async def test_same_session_resumes_and_other_owner_has_no_cookies(self):
        async with self.runtime.lease(self.identity, "first", 1, 2) as first:
            await first.operator.page.context.add_cookies([{"name":"fake_login","value":"alice","url":"http://fake.test"}])
            page = first.operator.page
        async with self.runtime.lease({**self.identity,"owner_id":"bob"}, "other", 1, 2) as other:
            self.assertEqual(await other.operator.page.context.cookies(), [])
            self.assertIsNot(other.operator.page, page)
        async with self.runtime.lease(self.identity, "resume", 1, 2) as resumed:
            self.assertIs(resumed.operator.page, page)
            self.assertEqual((await page.context.cookies())[0]["value"], "alice")

    async def test_gui_transport_uses_the_shared_runtime_queue(self):
        from service.web_socket import server
        from starlette.websockets import WebSocketState
        from types import SimpleNamespace
        from unittest.mock import AsyncMock, patch
        frames = []
        started = asyncio.Event()
        finish = asyncio.Event()
        async def execute(*args, **kwargs):
            started.set()
            await finish.wait()
            return {"status":"END","outcome":"done","result":"fake done"}
        def build(operator, emit, planner):
            self.assertIs(operator.page, page)
            return SimpleNamespace(ainvoke=execute)
        ws = SimpleNamespace(client_state=WebSocketState.CONNECTED, send_json=AsyncMock(side_effect=frames.append))
        message = {"type":"run", "session_id":"gui-call", "identity":self.identity,
            "instruction":"fake task", "max_steps":1, "queue_timeout":2,"execution_timeout":2,
            "model_config":{"base_url":"http://fake.test/v1","api_key":"fake","model":"fake","vision_verified":True}}
        with patch.object(server, "_runtime", self.runtime), patch.object(server, "build", build):
            async with self.runtime.lease(self.identity, "browser-first", 1, 2) as lease:
                page = lease.operator.page
                gui = asyncio.create_task(server.run_task(ws, message))
                await asyncio.sleep(.03)
                self.assertFalse(started.is_set())
            await asyncio.wait_for(started.wait(), 1)
            finish.set()
            await gui
        self.assertEqual(frames[-1]["type"], "done", frames)

    async def test_cancel_during_page_acquisition_settles_before_queue_release(self):
        from service.browser_runtime import BrowserRuntime
        from types import SimpleNamespace
        entered, finish = asyncio.Event(), asyncio.Event()
        original, _ = await self.pool.get("alice", "one")
        async def slow_get(*args):
            entered.set()
            await finish.wait()
            return original, True
        runtime = BrowserRuntime(SimpleNamespace(get=slow_get))
        async def work():
            async with runtime.lease(self.identity, "slow", 1, 2):
                self.fail("cancelled acquisition became active")
        task = asyncio.create_task(work())
        await entered.wait()
        task.cancel()
        await asyncio.sleep(.02)
        self.assertFalse(task.done(), "queue released before page acquisition settled")
        finish.set()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertTrue(original.page.is_closed())

    async def test_repeated_cancel_cannot_interrupt_page_cleanup(self):
        from unittest.mock import patch
        started, finish = asyncio.Event(), asyncio.Event()
        original, _ = await self.pool.get("alice", "one")
        real_close = original.page.close
        async def slow_close():
            started.set()
            await finish.wait()
            await real_close()
        async def work():
            async with self.runtime.lease(self.identity, "cancel", 1, 3, "gui"):
                await asyncio.Event().wait()
        with patch.object(original.page, "close", slow_close):
            task = asyncio.create_task(work())
            await asyncio.sleep(.03)
            task.cancel()
            await started.wait()
            task.cancel()
            task.cancel()
            await asyncio.sleep(.01)
            self.assertFalse(task.done())
            finish.set()
            with self.assertRaises(asyncio.CancelledError):
                await task
        self.assertTrue(original.page.is_closed())
