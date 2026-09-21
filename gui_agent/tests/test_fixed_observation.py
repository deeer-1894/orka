"""Real fixed-script dispatch, cancellation and protected-world contracts."""
import asyncio
import unittest
from unittest.mock import patch

from playwright.async_api import async_playwright
from operators.sessions import SessionPool
from service.browser_bridge import PageChannel, BridgeError
from service.browser_runtime import BrowserRuntime
from service.browser_observation import SCRIPTS, by_value


class FixedObservationTests(unittest.IsolatedAsyncioTestCase):
    def test_exception_objects_never_grant_a_protected_world_handle(self):
        result = by_value({"exceptionDetails": {"exception": {"objectId": "private-handle"}}})
        self.assertNotIn("private-handle", str(result))

    async def asyncSetUp(self):
        self.pw = await async_playwright().start()
        self.browser = await self.pw.chromium.launch(args=["--no-sandbox"])
        self.pool = SessionPool(browser=self.browser)
        self.runtime = BrowserRuntime(self.pool)
        self.who = {"owner_id": "fixture", "conversation_id": "fixed-observation", "run_id": "run"}

    async def asyncTearDown(self):
        await self.pool.close()
        await self.browser.close()
        await self.pw.stop()

    async def test_pending_fixed_snapshot_and_settle_cancel_preserve_page(self):
        for operation in ("snapshot", "settle"):
            with self.subTest(operation=operation):
                started = asyncio.Event()
                async def work():
                    async with self.runtime.lease(self.who, "observe", 1, 3) as lease:
                        self.page = lease.operator.page
                        self.page_id = lease.state.page_id
                        await self.page.set_content('<p>Keep context</p><input value="draft">')
                        cdp = await lease.cdp()
                        original = cdp.send
                        async def pending(method, params=None):
                            result = await original(method, params)
                            is_snapshot = (method == "Runtime.callFunctionOn" and params.get("functionDeclaration") == SCRIPTS["snapshot"])
                            is_settle = (method == "Runtime.evaluate" and params.get("expression") == SCRIPTS["settle"])
                            if (operation == "snapshot" and is_snapshot) or (operation == "settle" and is_settle):
                                started.set()
                                # The real fixed script ran; its transport reply
                                # remains unconfirmed until the caller cancels.
                                await asyncio.Event().wait()
                            return result
                        with patch.object(cdp, "send", pending):
                            await PageChannel(lease).execute({"id": 1, "method": "Orka.observe", "params": {"operation": operation}})
                task = asyncio.create_task(work())
                await asyncio.wait_for(started.wait(), 2)
                task.cancel()
                with self.assertRaises(asyncio.CancelledError):
                    await task
                self.assertFalse(self.page.is_closed())
                async with self.runtime.lease(self.who, "next", 1, 2) as lease:
                    self.assertEqual(lease.state.page_id, self.page_id)
                    self.assertEqual(await lease.operator.page.input_value("input"), "draft")
                    reply = await PageChannel(lease).execute({"id": 1, "method": "Orka.observe", "params": {"operation": "snapshot"}})
                    self.assertIn("Keep context", reply["result"]["value"]["snapshot"]["text"])

    async def test_arbitrary_evaluate_cannot_poison_fixed_observer_world(self):
        async with self.runtime.lease(self.who, "isolation", 1, 3) as lease:
            await lease.operator.page.set_content("<p>Original</p><button>Safe</button>")
            channel = PageChannel(lease)
            cdp = await lease.cdp()
            tree = await cdp.send("Page.getFrameTree")
            frame = tree["frameTree"]["frame"]["id"]
            world = await channel.execute({"id": 1, "method": "Page.createIsolatedWorld", "params": {"frameId": frame, "worldName": "orka-browser"}})
            await channel.execute({"id": 2, "method": "Runtime.evaluate", "params": {
                "contextId": world["executionContextId"],
                "expression": "globalThis.__orkaObserve=()=>{document.body.textContent='poisoned'}; globalThis.MutationObserver=class{constructor(){document.body.textContent='poisoned'}}",
            }})
            observed = await channel.execute({"id": 3, "method": "Orka.observe", "params": {"operation": "snapshot"}})
            self.assertIn("Original", observed["result"]["value"]["snapshot"]["text"])
            await channel.execute({"id": 4, "method": "Orka.observe", "params": {"operation": "settle"}})
            self.assertEqual(await lease.operator.page.locator("p").inner_text(), "Original")
            for seq, method, params in (
                (5, "Page.createIsolatedWorld", {"frameId": frame, "worldName": "orka-observer"}),
                (6, "Runtime.evaluate", {"contextId": lease.state.observation_world, "expression": "1"}),
                (7, "Orka.observe", {"operation": "fill", "request": {"selector": "input", "text": "bad"}}),
                (8, "Orka.observe", {"operation": "snapshot", "expression": "while(true){}"}),
                (9, "Orka.observe", {"operation": "snapshot", "request": {"operation": "fill"}}),
            ):
                with self.assertRaises(BridgeError):
                    await channel.execute({"id": seq, "method": method, "params": params})

    async def test_cancelled_commit_restores_last_delivered_baseline(self):
        async with self.runtime.lease(self.who, "initial", 1, 2) as lease:
            await lease.operator.page.set_content("<p>Before</p>")
            channel = PageChannel(lease)
            first = await channel.execute({"id": 1, "method": "Orka.observe", "params": {"operation": "snapshot"}})
            snapshot_id = first["result"]["value"]["snapshot"]["id"]
            await channel.execute({"id": 2, "method": "Orka.observe", "params": {"operation": "commit_observation", "request": {"snapshot_id": snapshot_id}}})
            lease.release_requested = True
        lease.state.observation_delivery = ""  # Delivered initial receipt.
        pending = asyncio.Event()
        async def commit():
            async with self.runtime.lease(self.who, "cancel-commit", 1, 3) as lease:
                await lease.operator.page.locator("p").evaluate("node => node.textContent = 'After'")
                channel = PageChannel(lease)
                candidate = await channel.execute({"id": 1, "method": "Orka.observe", "params": {"operation": "snapshot", "request": {"action": "wait"}}})
                cdp = await lease.cdp()
                original = cdp.send
                async def unconfirmed(method, params=None):
                    result = await original(method, params)
                    if method == "Runtime.callFunctionOn" and "leaseCommit" in params.get("functionDeclaration", ""):
                        pending.set()
                        await asyncio.Event().wait()
                    return result
                with patch.object(cdp, "send", unconfirmed):
                    await channel.execute({"id": 2, "method": "Orka.observe", "params": {"operation": "commit_observation", "request": {"snapshot_id": candidate["result"]["value"]["snapshot"]["id"]}}})
        task = asyncio.create_task(commit())
        await asyncio.wait_for(pending.wait(), 2)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        async with self.runtime.lease(self.who, "observe-again", 1, 2) as lease:
            observed = await PageChannel(lease).execute({"id": 1, "method": "Orka.observe", "params": {"operation": "snapshot", "request": {"action": "wait"}}})
            value = observed["result"]["value"]
            self.assertEqual(value["snapshot"]["mode"], "full")
            self.assertIn("After", value["snapshot"]["text"])

    async def test_release_and_delivery_ack_loss_force_full_without_losing_redactions(self):
        from service.browser_bridge import serve_bridge
        from tests.test_browser_bridge import Socket
        from starlette.websockets import WebSocketDisconnect
        for loss in ("release", "ack", "none"):
            with self.subTest(loss=loss):
                who = {**self.who, "conversation_id": "receipt-" + loss}
                ws = Socket()
                send = ws.send_json
                async def maybe_drop(value):
                    if loss == "release" and value.get("type") == "released":
                        raise WebSocketDisconnect()
                    await send(value)
                ws.send_json = maybe_drop
                task = asyncio.create_task(serve_bridge(ws, self.runtime))
                await ws.send({"type": "acquire", "identity": who, "operation_id": "receipt", "queue_timeout": 1, "execution_timeout": 5})
                await ws.read()  # queued
                acquired = await ws.read()
                scope = {key: acquired[key] for key in ("identity", "operation_id", "lease_id")}
                operator, _ = await self.pool.get(who["owner_id"], who["conversation_id"])
                await operator.page.set_content('<input id="note" oninput="document.querySelector(\'p\').textContent=this.value"><p>Before</p>')
                async def command(seq, method, params):
                    await ws.send({"type": "command", **scope, "id": seq, "method": method, "params": params})
                    result = await ws.read()
                    self.assertNotIn("error", result)
                    return result["result"]
                await command(1, "Orka.act", {"operation": "fill", "request": {"selector": "#note", "text": "fixture-private-note"}})
                observed = await command(2, "Orka.observe", {"operation": "snapshot", "request": {"action": "wait"}})
                await command(3, "Orka.observe", {"operation": "commit_observation", "request": {"snapshot_id": observed["result"]["value"]["snapshot"]["id"]}})
                await ws.send({"type": "release", **scope})
                if loss != "release":
                    self.assertEqual((await ws.read())["type"], "released")
                    if loss == "none":
                        await ws.send({"type": "observation_ack", **scope})
                    await ws.incoming.put(None)
                await asyncio.wait_for(task, 3)
                async with self.runtime.lease(who, "next", 1, 2) as lease:
                    self.assertEqual(lease.state.page_id, acquired["page_id"])
                    result = await PageChannel(lease).execute({"id": 1, "method": "Orka.observe", "params": {"operation": "snapshot", "request": {"action": "wait"}}})
                    snapshot = result["result"]["value"]["snapshot"]
                    self.assertEqual(snapshot["mode"], "unchanged" if loss == "none" else "full")
                    self.assertNotIn("fixture-private-note", snapshot["text"])
                    if loss != "none":
                        self.assertIn("[redacted]", snapshot["text"])
