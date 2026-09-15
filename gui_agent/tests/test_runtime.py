"""Offline isolation and runtime contract; never attach to a user's browser."""
import asyncio
import importlib.util
import inspect
import json
import os
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import MagicMock
from service.browser_runtime import BrowserRuntime
from unittest.mock import AsyncMock, patch

from agent.model import Planner, UITarsPlanner
from test_som_planner import FakeClient, response


class RuntimeContractTests(unittest.IsolatedAsyncioTestCase):
    async def test_missing_model_config_never_uses_environment_credentials(self):
        fake = FakeClient(response('{"action":"done","result":"done"}'))
        with patch.dict(os.environ, {"GUI_PLANNER": "vlm", "OPENAI_API_KEY": "global-secret", "OPENAI_BASE_URL": "http://global.invalid/v1"}), patch("openai.AsyncOpenAI", return_value=fake) as sdk:
            result, _ = await Planner().predict({"instruction": "read", "screenshot": "cG5n"}, None)
        self.assertEqual(result["action"], "error")
        sdk.assert_not_called()

    async def test_uitars_transport_is_async(self):
        self.assertTrue(inspect.iscoroutinefunction(UITarsPlanner._chat))

    async def test_ws_requires_authenticated_private_service(self):
        from service.web_socket import server
        ws = SimpleNamespace(headers={}, query_params={}, client=SimpleNamespace(host="127.0.0.1"))
        with patch.object(server, "_GUI_AUTH_TOKEN", ""):
            self.assertFalse(server._authorized(ws))

    async def test_macros_never_persist_sensitive_parameters(self):
        from agent.macro import MacroStore
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "macros.json"
            store = MacroStore(str(path))
            store.put("login with fake-secret", [{"action": "type", "selector": "#password", "text": "fake-secret"}])
            self.assertFalse(path.exists())
            self.assertIsNone(store.get("login with fake-secret"))

    async def test_queue_is_bounded_and_cancelled_waiter_releases_capacity(self):
        self.assertIsNotNone(importlib.util.find_spec("service.runtime"), "bounded queue is missing")
        from service.runtime import ExecutionQueue, QueueFull
        queue = ExecutionQueue(max_waiters=1)
        entered = asyncio.Event()
        async def wait():
            async with queue.slot(10):
                entered.set()
        async with queue.slot(1):
            task = asyncio.create_task(wait())
            await asyncio.sleep(0)
            with self.assertRaises(QueueFull):
                async with queue.slot(1):
                    pass
            task.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await task
            self.assertEqual(queue.waiting, 0)
        async with queue.slot(1):
            self.assertFalse(entered.is_set())

    async def test_queue_timeout_does_not_take_execution_slot(self):
        self.assertIsNotNone(importlib.util.find_spec("service.runtime"), "bounded queue is missing")
        from service.runtime import ExecutionQueue
        queue = ExecutionQueue(max_waiters=2)
        async with queue.slot(1):
            async def wait():
                async with queue.slot(0.01):
                    self.fail("queue deadline ignored")
            with self.assertRaises(TimeoutError):
                await wait()
        async with queue.slot(1):
            self.assertEqual(queue.waiting, 0)


class BrowserSessionTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.assertIsNotNone(importlib.util.find_spec("operators.sessions"), "isolated session pool is missing")
        from operators.sessions import SessionPool
        from playwright.async_api import async_playwright
        self.pw = await async_playwright().start()
        self.browser = await self.pw.chromium.launch(headless=os.getenv("GUI_TEST_HEADFUL") != "1", args=["--no-sandbox"])
        self.pool = SessionPool(browser=self.browser, max_sessions=3)

    async def asyncTearDown(self):
        if hasattr(self, "pool"):
            await self.pool.close()
            await self.browser.close()
            await self.pw.stop()

    async def test_real_cookies_and_storage_isolate_owner_and_conversation_and_resume(self):
        first, resumed = await self.pool.get("owner-a", "conversation-1")
        self.assertFalse(resumed)
        await first.page.route("http://fake.test/**", lambda route: route.fulfill(body="<input id=entry>", content_type="text/html"))
        await first.page.goto("http://fake.test/form")
        await first.page.context.add_cookies([{"name":"fake_login", "value":"owner-a", "url":"http://fake.test"}])
        await first.page.evaluate("localStorage.setItem('fake_account','owner-a')")
        await first.page.fill("#entry", "continued-value")
        for owner, conversation in [("owner-b", "conversation-1"), ("owner-a", "conversation-2")]:
            other, _ = await self.pool.get(owner, conversation)
            await other.page.route("http://fake.test/**", lambda route: route.fulfill(body="<input id=entry>", content_type="text/html"))
            await other.page.goto("http://fake.test/form")
            self.assertEqual(await other.page.context.cookies(), [])
            self.assertIsNone(await other.page.evaluate("localStorage.getItem('fake_account')"))
        again, resumed = await self.pool.get("owner-a", "conversation-1")
        self.assertTrue(resumed)
        self.assertIs(again.page, first.page)
        self.assertEqual(await again.page.input_value("#entry"), "continued-value")
        self.assertEqual((await again.page.context.cookies())[0]["value"], "owner-a")

    async def test_pool_is_bounded_and_eviction_reports_new_session(self):
        for n in range(5):
            await self.pool.get("owner", str(n))
        self.assertLessEqual(len(self.browser.contexts), 3)
        _, resumed = await self.pool.get("owner", "0")
        self.assertFalse(resumed)

    async def test_pool_never_adopts_or_closes_an_external_context(self):
        external = await self.browser.new_context()
        page = await external.new_page()
        await external.add_cookies([{"name":"external_fake_account","value":"external","url":"http://fake.test"}])
        operator, _ = await self.pool.get("owner","new-session")
        self.assertEqual(await operator.page.context.cookies(), [])
        self.assertIsNot(operator.page.context, external)
        await self.pool.close()
        self.assertFalse(page.is_closed())
        await external.close()

    @unittest.skipUnless(os.getenv("GUI_TEST_HEADFUL") == "1", "requires isolated Xvfb display")
    async def test_headful_active_session_has_visible_window(self):
        first, _ = await self.pool.get("owner","visible-one")
        await first.page.set_content("<h1>fake session one</h1>")
        second, _ = await self.pool.get("owner","visible-two")
        await second.page.set_content("<h1>fake session two</h1>")
        active, _ = await self.pool.get("owner","visible-one")
        self.assertTrue(await active.page.evaluate("document.hasFocus()"))
        cdp = await active.page.context.new_cdp_session(active.page)
        window = await cdp.send("Browser.getWindowForTarget")
        self.assertIn(window["bounds"]["windowState"], ("normal", "maximized", "fullscreen"))
        self.assertGreater(len(await active.page.screenshot()), 100)


class WebSocketLifecycleTests(unittest.IsolatedAsyncioTestCase):
    def message(self, **overrides):
        return {"type":"run", "session_id":"call", "identity":{"owner_id":"a","conversation_id":"c","run_id":"r"},
            "model_config":{"base_url":"http://fake.test/v1","api_key":"fake-key","model":"fake-model","vision_verified":True},
            "instruction":"read", "queue_timeout":1, "execution_timeout":1, **overrides}

    async def test_execution_timeout_retains_receipts_usage_and_releases_slot(self):
        from service.web_socket import server
        from service.runtime import ExecutionQueue
        from starlette.websockets import WebSocketState
        frames = []
        async def emit(frame): frames.append(frame)
        ws = SimpleNamespace(client_state=WebSocketState.CONNECTED, send_json=emit)
        pool = SimpleNamespace(get=AsyncMock(return_value=(SimpleNamespace(page=MagicMock(close=AsyncMock())),True)))
        def build(operator, emit, planner):
            async def run(*args, **kwargs):
                await emit({"type":"action","action":"read","result":"read returned"})
                await emit({"type":"progress","task_memory":{"goals":[{"goal":"initial","observation":"Value 17"}]}})
                await planner.usage.record("fake-model", SimpleNamespace(prompt_tokens=3,completion_tokens=2,total_tokens=5))
                await asyncio.Event().wait()
            return SimpleNamespace(ainvoke=run)
        queue = ExecutionQueue()
        with patch.object(server,"_runtime",BrowserRuntime(pool,queue)), patch.object(server,"build",build):
            await server.run_task(ws,self.message(execution_timeout=0.02))
        self.assertEqual(frames[-1]["outcome"],"partial")
        self.assertEqual(frames[-1]["phase"],"execution")
        self.assertEqual(frames[-1]["usage"]["total_tokens"],5)
        self.assertEqual(frames[-1]["task_memory"]["goals"][0]["observation"],"Value 17")
        self.assertTrue(any(f["type"] == "action" for f in frames))
        async with queue.slot(0.1): pass
        self.assertNotIn("fake-key",json.dumps(frames))

    async def test_disconnect_cancels_active_runner_and_releases_slot(self):
        from service.web_socket import server
        from service.runtime import ExecutionQueue
        from starlette.websockets import WebSocketState
        started, cancelled = asyncio.Event(), asyncio.Event()
        ws = SimpleNamespace(client_state=WebSocketState.CONNECTED, accept=AsyncMock(),send_json=AsyncMock(),receive_json=AsyncMock(return_value=self.message()))
        async def receive():
            await started.wait()
            ws.client_state = WebSocketState.DISCONNECTED
            return {"type":"websocket.disconnect"}
        ws.receive = receive
        async def run(*args,**kwargs):
            started.set()
            try: await asyncio.Event().wait()
            finally: cancelled.set()
        queue = ExecutionQueue()
        pool = SimpleNamespace(get=AsyncMock(return_value=(SimpleNamespace(page=MagicMock(close=AsyncMock())),True)))
        with patch.object(server,"_authorized",return_value=True), patch.object(server,"_runtime",BrowserRuntime(pool,queue)), patch.object(server,"build",return_value=SimpleNamespace(ainvoke=run)):
            await asyncio.wait_for(server.gui_ws(ws),1)
        self.assertTrue(cancelled.is_set())
        async with queue.slot(0.1): pass

    async def test_emitter_is_scoped_and_closed_after_done_or_cancel(self):
        from service.web_socket import server
        from service.runtime import ExecutionQueue
        from starlette.websockets import WebSocketState
        for cancel in (False, True):
            with self.subTest(cancel=cancel):
                frames, callbacks = [], []
                started = asyncio.Event()
                async def send(frame): frames.append(frame)
                ws = SimpleNamespace(client_state=WebSocketState.CONNECTED, send_json=send)
                pool = SimpleNamespace(get=AsyncMock(return_value=(SimpleNamespace(page=MagicMock(close=AsyncMock())), True)))
                def build(operator, emit, planner):
                    callbacks.append(emit)
                    async def run(*args, **kwargs):
                        await emit({"type":"screenshot", "data":"own-screen", "session_id":"spoof", "run_id":"spoof"})
                        started.set()
                        if cancel:
                            try: await asyncio.Event().wait()
                            finally: await emit({"type":"action", "action":"click", "target":"late-cancel"})
                        return {"status":"END", "outcome":"done", "result":"done"}
                    return SimpleNamespace(ainvoke=run)
                with patch.object(server,"_runtime",BrowserRuntime(pool,ExecutionQueue())), patch.object(server,"build",build):
                    task = asyncio.create_task(server.run_task(ws,self.message()))
                    await started.wait()
                    if cancel:
                        task.cancel()
                        with self.assertRaises(asyncio.CancelledError): await task
                    else: await task
                    count = len(frames)
                    await callbacks[0]({"type":"screenshot", "data":"late-screen"})
                    self.assertEqual(len(frames),count,"finished run emitted into a reused connection")
                self.assertFalse(any(frame.get("target") == "late-cancel" for frame in frames))
                for frame in frames:
                    self.assertEqual(frame["session_id"],"call")
                    self.assertEqual(frame["run_id"],"r")

    async def test_invalid_identity_does_not_get_a_session_or_leak_configuration(self):
        from service.web_socket import server
        from starlette.websockets import WebSocketState
        ws = SimpleNamespace(client_state=WebSocketState.CONNECTED,send_json=AsyncMock())
        pool = SimpleNamespace(get=AsyncMock())
        with patch.object(server,"_runtime",BrowserRuntime(pool)):
            await server.run_task(ws,self.message(identity={}))
        pool.get.assert_not_awaited()
        self.assertEqual(ws.send_json.call_args.args[0]["phase"],"validation")
        self.assertNotIn("fake-key",str(ws.send_json.call_args))


class PlannerWindowTests(unittest.IsolatedAsyncioTestCase):
    async def test_one_shot_window_stays_bounded(self):
        from agent.graph import build
        from test_uitars import b64_png
        states = []
        async def predict(state,page):
            states.append(state)
            return ({"action":"done","result":"done"} if len(states)==4 else {"action":"type","selector":"#a","text":str(len(states))}), True
        planner = SimpleNamespace(mode="uitars",uitars=SimpleNamespace(max_shots=1),predict=predict)
        op = SimpleNamespace(page=SimpleNamespace(url="about:blank"), screenshot=AsyncMock(return_value=b64_png(100,100)), dom_snapshot=AsyncMock(return_value="form"),execute=AsyncMock(return_value="returned"),input_is_sensitive=AsyncMock(return_value=False))
        await build(op,AsyncMock(),planner=planner).ainvoke({"max_steps":10,"history":[],"step":0})
        self.assertTrue(all(len(state["shots"])==1 for state in states))
