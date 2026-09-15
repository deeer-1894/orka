"""Wire-level CDP contracts using a real, isolated Chromium (no providers)."""
import asyncio
import json
import unittest
from playwright.async_api import async_playwright
from operators.sessions import SessionPool
from starlette.websockets import WebSocketDisconnect


class Socket:
    def __init__(self):
        self.incoming, self.outgoing = asyncio.Queue(), asyncio.Queue()
    async def receive_text(self):
        value = await self.incoming.get()
        if value is None:
            raise WebSocketDisconnect()
        return value
    async def send_json(self, value):
        await self.outgoing.put(value)
    async def send(self, value):
        await self.incoming.put(json.dumps(value))
    async def read(self):
        return await asyncio.wait_for(self.outgoing.get(), 4)


class BridgeTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        from service.browser_runtime import BrowserRuntime
        from service.browser_bridge import serve_bridge
        self.pw = await async_playwright().start()
        self.browser = await self.pw.chromium.launch(args=["--no-sandbox"])
        self.pool = SessionPool(browser=self.browser)
        self.runtime = BrowserRuntime(self.pool)
        self.ws = Socket()
        self.task = asyncio.create_task(serve_bridge(self.ws, self.runtime))
        self.identity = {"owner_id":"alice", "conversation_id":"one", "run_id":"r1"}
        self.acquire = {"type":"acquire", "identity":self.identity, "operation_id":"op", "queue_timeout":2, "execution_timeout":10}
        self.seq = 0

    async def asyncTearDown(self):
        if hasattr(self, "task"):
            await self.ws.incoming.put(None)
            await asyncio.wait_for(self.task, 4)
            await self.pool.close()
            await self.browser.close()
            await self.pw.stop()

    async def connect(self, **kwargs):
        await self.ws.send({**self.acquire, **kwargs})
        self.assertEqual((await self.ws.read())["type"], "queued")
        frame = await self.ws.read()
        self.assertEqual(frame["type"], "acquired", frame)
        self.scope = {k:frame[k] for k in ("identity","operation_id","lease_id")}
        return frame

    async def command(self, method, params=None, **kwargs):
        self.seq += 1
        await self.ws.send({"type":"command", **self.scope, "id":self.seq, "method":method, "params":params or {}, **kwargs})
        return await self.ws.read()

    async def world(self):
        frame = await self.command("Page.getFrameTree")
        frame_id = frame["result"]["frameTree"]["frame"]["id"]
        world = await self.command("Page.createIsolatedWorld", {"frameId":frame_id,"worldName":"orka-browser"})
        return world["result"]["executionContextId"]

    async def test_scoped_commands_evaluate_and_idempotent_release(self):
        info = await self.connect()
        context = await self.world()
        reply = await self.command("Runtime.evaluate", {"contextId":context,"expression":"document.body.innerHTML='<input>'; 6*7", "returnByValue":True})
        self.assertEqual(reply["type"], "reply")
        self.assertEqual(reply["result"]["result"]["value"], 42)
        self.assertEqual(reply["page_id"], info["page_id"])
        self.assertEqual(reply["identity"], self.identity)
        for _ in range(2):
            await self.ws.send({"type":"release", **self.scope})
            self.assertEqual((await self.ws.read())["type"], "released")
        async with self.runtime.lease(self.identity, "gui", 1, 1, "gui") as lease:
            self.assertEqual(await lease.operator.page.locator("input").count(), 1)
            self.assertGreater(lease.state.epoch, info["page_epoch"])

    async def test_forged_scope_and_root_or_main_world_never_dispatch(self):
        await self.connect()
        for method, params in [("Browser.close",{}), ("Target.getTargets",{}),
                               ("Runtime.evaluate",{"expression":"document.body.textContent='bad'"}),
                               ("Runtime.evaluate",{"contextId":999,"expression":"1"})]:
            reply = await self.command(method, params)
            self.assertIn("error", reply)
        reply = await self.command("Page.enable", identity={**self.identity,"owner_id":"bob"})
        self.assertEqual(reply["error"]["code"], "scope_mismatch")
        self.assertEqual(reply["identity"], self.identity)
        self.assertTrue(self.browser.is_connected())

    async def test_cancel_inflight_script_closes_page_before_ack_and_next_lease(self):
        await self.connect()
        context = await self.world()
        page, _ = await self.pool.get("alice", "one")
        await self.ws.send({"type":"command", **self.scope,"id":3,"method":"Runtime.evaluate",
            "params":{"contextId":context,"expression":"while(true) {}", "awaitPromise":True}})
        await asyncio.sleep(.05)
        await self.ws.send({"type":"cancel", **self.scope})
        reply = await self.ws.read()
        self.assertEqual(reply["type"], "cancelled", reply)
        self.assertTrue(page.page.is_closed())
        async with self.runtime.lease(self.identity, "next", 1, 2) as lease:
            self.assertIsNot(lease.operator.page, page.page)
            self.assertEqual(await lease.operator.page.evaluate("21*2"), 42)

    async def test_disconnect_during_command_releases_only_after_cleanup(self):
        await self.connect()
        context = await self.world()
        page, _ = await self.pool.get("alice", "one")
        await self.ws.send({"type":"command", **self.scope,"id":3,"method":"Runtime.evaluate",
            "params":{"contextId":context,"expression":"new Promise(() => {})", "awaitPromise":True}})
        await asyncio.sleep(.03)
        await self.ws.incoming.put(None)
        await asyncio.wait_for(self.task, 3)
        self.assertTrue(page.page.is_closed())
        async with self.runtime.lease(self.identity, "after", 1, 1):
            pass

    async def test_execution_expiry_and_output_script_message_limits(self):
        await self.connect(execution_timeout=.5)
        context = await self.world()
        reply = await self.command("Runtime.evaluate", {"contextId":context,"expression":"'x'.repeat(140000)","returnByValue":True})
        self.assertEqual(reply["error"]["code"], "output_limit")
        reply = await self.command("Runtime.evaluate", {"contextId":context,"expression":" "*17000})
        self.assertEqual(reply["error"]["code"], "input_limit")
        expired = await self.ws.read()
        self.assertEqual(expired["error"]["code"], "timeout")
        reply = await self.command("Page.enable")
        self.assertEqual(reply["error"]["code"], "lease_expired")

    async def test_oversized_message_rejected_before_acquisition(self):
        await self.ws.incoming.put(" "*131073)
        reply = await self.ws.read()
        self.assertEqual(reply["error"]["code"], "input_limit")
        self.assertEqual(len(self.browser.contexts), 0)

    async def test_command_limit_and_two_commands_inflight(self):
        await self.connect()
        for _ in range(256):
            result = await self.command("Page.enable")
            self.assertIn("result", result)
        result = await self.command("Page.enable")
        self.assertEqual(result["error"]["code"], "command_limit")

    async def test_second_command_is_rejected_while_first_waits(self):
        await self.connect()
        context = await self.world()
        await self.ws.send({"type":"command", **self.scope,"id":3,"method":"Runtime.evaluate",
            "params":{"contextId":context,"expression":"new Promise(() => {})", "awaitPromise":True}})
        await self.ws.send({"type":"command", **self.scope,"id":4,"method":"Page.enable","params":{}})
        reply = await self.ws.read()
        self.assertEqual(reply["id"], 4)
        self.assertEqual(reply["error"]["code"], "protocol_error")
        await self.ws.send({"type":"cancel", **self.scope})
        self.assertEqual((await self.ws.read())["type"], "cancelled")

    async def test_queue_timeout_and_queued_disconnect_never_acquire_page(self):
        async with self.runtime.lease(self.identity, "gui", 1, 2, "gui"):
            await self.ws.send({**self.acquire,"queue_timeout":.02})
            self.assertEqual((await self.ws.read())["type"], "queued")
            self.assertEqual((await self.ws.read())["error"]["code"], "timeout")
        self.assertEqual(len(self.browser.contexts), 1)

    async def test_cdp_object_and_world_scope_are_invalidated_on_gui_takeover(self):
        await self.connect()
        context = await self.world()
        obj = await self.command("Runtime.evaluate", {"contextId":context,"expression":"document.body"})
        object_id = obj["result"]["result"]["objectId"]
        await self.ws.send({"type":"release", **self.scope})
        self.assertEqual((await self.ws.read())["type"], "released")
        from service.browser_bridge import PageChannel, BridgeError
        async with self.runtime.lease(self.identity, "gui", 1, 2, "gui"):
            pass
        async with self.runtime.lease(self.identity, "next", 1, 2) as lease:
            channel = PageChannel(lease)
            with self.assertRaises(BridgeError):
                await channel.execute({"id":1,"method":"Runtime.evaluate","params":{"contextId":context,"expression":"1"}})
            with self.assertRaises(BridgeError):
                await channel.execute({"id":2,"method":"Runtime.getProperties","params":{"objectId":object_id}})

    async def test_world_universal_access_and_non_http_navigation_are_rejected(self):
        await self.connect()
        tree = await self.command("Page.getFrameTree")
        main = tree["result"]["frameTree"]["frame"]["id"]
        for params in ({"frameId":main,"worldName":"orka-browser","grantUniveralAccess":True},
                       {"frameId":"foreign","worldName":"orka-browser"},
                       {"frameId":main,"worldName":"main"}):
            self.assertIn("error", await self.command("Page.createIsolatedWorld", params))
        for url in ("file:///etc/passwd", "javascript:1", "data:text/html,bad"):
            self.assertIn("error", await self.command("Page.navigate", {"url":url}))

    async def test_generated_cdproto_false_defaults_do_not_expand_privileges(self):
        await self.connect()
        reply = await self.command("Page.enable", {"enableFileChooserOpenedEvent":False})
        self.assertIn("result", reply)
        context = await self.world()
        reply = await self.command("Runtime.evaluate", {"contextId":context,"expression":"42","replMode":False,"returnByValue":True})
        self.assertEqual(reply["result"]["result"]["value"], 42)
        self.assertIn("error", await self.command("Page.enable", {"enableFileChooserOpenedEvent":True}))
        self.assertIn("error", await self.command("Runtime.evaluate", {"contextId":context,"expression":"42","replMode":True}))
