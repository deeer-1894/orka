"""A multi-stage GUI task must retain readouts after the visible state changes."""
import json
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock

from agent.graph import build


class TaskMemoryGraphTests(unittest.IsolatedAsyncioTestCase):
    async def run_graph(self, actions, max_steps=20, sensitive=False):
        states, frames = [], []
        replies = iter(actions)
        async def predict(state, page):
            states.append(dict(state))
            return next(replies), False
        operator = SimpleNamespace(
            page=SimpleNamespace(url="https://fake.test/table"),
            screenshot=AsyncMock(return_value="cG5n"),
            dom_snapshot=AsyncMock(return_value="Canvas report; numeric values exist only in the screenshot"),
            execute=AsyncMock(return_value="operator returned"),
            input_is_sensitive=AsyncMock(return_value=sensitive), title=AsyncMock(return_value="Report"),
        )
        async def emit(frame): frames.append(frame)
        result = await build(operator, emit, planner=SimpleNamespace(mode="rule",predict=predict)).ainvoke(
            {"instruction":"Read initial and changed values, then restore initial view", "history":[],"step":0,"max_steps":max_steps},
            config={"recursion_limit":100})
        return result, states, frames

    async def test_initial_and_changed_readouts_survive_restoration(self):
        result, states, frames = await self.run_graph([
            {"action":"click", "selector":"#change", "progress":{"goal":"initial values","status":"complete","observation":"Value 17"}},
            {"action":"click", "selector":"#restore", "progress":{"goal":"changed values","status":"complete","observation":"Value 26"}},
            {"action":"done", "result":"Initial 17, changed 26; restored", "progress":{"goal":"restore","status":"complete","observation":"Initial view visible again"}},
        ])
        memory = states[-1].get("task_memory", {})
        goals = memory.get("goals", [])
        self.assertEqual([(g["goal"],g["observation"]) for g in goals],[("initial values","Value 17"),("changed values","Value 26")])
        self.assertEqual([g["observed_step"] for g in goals],[0,1])
        self.assertEqual([g["observation_seq"] for g in goals],[1,3])
        self.assertTrue(all(g["source"] == "model_observation" for g in goals))
        self.assertEqual(len(result["task_memory"]["goals"]),3)
        progress = [f for f in frames if f["type"] == "progress"]
        self.assertEqual(len(progress),3)
        self.assertIn("not acceptance",progress[-1]["task_memory"]["note"])
        receipts = [f["evidence"] for f in frames if f["type"] == "action"]
        self.assertNotIn("Value 17",json.dumps(receipts))

    async def test_memory_is_bounded_and_keeps_latest_goal_status(self):
        actions = [{"action":"click","selector":f"#item-{i}","progress":{"goal":f"goal-{i}","status":"pending","observation":"x"*5000,"_raw":"never-copy"}} for i in range(12)]
        actions.append({"action":"done","result":"partial report","progress":{"goal":"goal-11","status":"complete","observation":"final reading"}})
        result, _, _ = await self.run_graph(actions)
        memory = result["task_memory"]
        self.assertEqual(len(memory["goals"]),8)
        self.assertEqual(memory["omitted_goals"],4)
        self.assertEqual(memory["goals"][-1]["status"],"complete")
        self.assertLess(len(json.dumps(memory)),18000)
        self.assertNotIn("never-copy",json.dumps(memory))

    async def test_sensitive_input_never_enters_checkpoint_and_later_echo_is_redacted(self):
        result, _, frames = await self.run_graph([
            {"action":"type","selector":"#password","text":"fake-secret","progress":{"goal":"private","status":"complete","observation":"fake-secret"}},
            {"action":"done","result":"done","progress":{"goal":"later","status":"complete","observation":"echo fake-secret"}},
        ],sensitive=True)
        self.assertEqual(len(result["task_memory"]["goals"]),1)
        self.assertNotIn("fake-secret",json.dumps([f for f in frames if f["type"]=="progress"]))

    async def test_budget_partial_keeps_readouts_and_new_invocation_starts_empty(self):
        result, _, _ = await self.run_graph([
            {"action":"click","selector":"#next","progress":{"goal":"first","status":"complete","observation":"Value 17"}},
        ],max_steps=1)
        self.assertEqual(result["outcome"],"partial")
        self.assertEqual(result["task_memory"]["goals"][0]["observation"],"Value 17")
        _, states, _ = await self.run_graph([{"action":"done","result":"new task"}])
        self.assertEqual(states[0]["task_memory"]["goals"],[])

    async def test_vlm_provider_receives_prior_phase_readouts_with_current_screenshot(self):
        from unittest.mock import patch
        from agent.model import Planner
        from agent.config import ModelConfig
        from test_som_planner import FakeClient, response
        fake = FakeClient(response('{"action":"done","result":"Value 17 then 26"}'))
        planner = Planner(ModelConfig("http://fake.test/v1","fake-key","any-visual-model",True),mode="vlm")
        memory={"goals":[{"goal":"first","status":"complete","observation":"Value 17","source":"model_observation","observed_step":0}],"omitted_goals":0}
        with patch("openai.AsyncOpenAI",return_value=fake):
            await planner.predict({"task_memory":memory,"screenshot":"CURRENT_ONLY"},None)
        messages=fake.request["messages"]
        content=messages[1]["content"]
        self.assertIn("Value 17",content[0]["text"])
        self.assertEqual([x["type"] for x in content],["text","image_url"])
        self.assertTrue(content[1]["image_url"]["url"].endswith("CURRENT_ONLY"))
        self.assertIn("DOM text only",messages[0]["content"])
        self.assertIn("progress",messages[0]["content"])

    async def test_unchanged_readout_update_keeps_original_screenshot_source(self):
        result, _, _ = await self.run_graph([
            {"action":"click","selector":"#change","progress":{"goal":"initial","status":"pending","observation":"Value 17"}},
            {"action":"done","result":"done","progress":{"goal":"initial","status":"complete","observation":"Value 17"}},
        ])
        goal=result["task_memory"]["goals"][0]
        self.assertEqual(goal["status"],"complete")
        self.assertEqual(goal["observed_step"],0)
        self.assertEqual(goal["observation_seq"],1)

    async def test_later_sensitive_classification_scrubs_final_memory_snapshot(self):
        result, _, frames = await self.run_graph([
            {"action":"read","progress":{"goal":"initial","status":"complete","observation":"fake-secret"}},
            {"action":"type","selector":"#password","text":"fake-secret"},
            {"action":"done","result":"done"},
        ],sensitive=True)
        self.assertNotIn("fake-secret",json.dumps(result["task_memory"]))
        latest=[f for f in frames if f["type"]=="progress"][-1]
        self.assertNotIn("fake-secret",json.dumps(latest))


class CanvasMemoryTests(unittest.IsolatedAsyncioTestCase):
    async def test_real_canvas_phases_survive_restore_through_async_provider(self):
        import asyncio
        import hashlib
        from playwright.async_api import async_playwright
        from agent.config import ModelConfig, ModelPolicy
        from agent.model import Planner
        from agent.provider import UsageLedger
        from operators.remote_browser import RemoteBrowserOperator
        requests, frames = [], []
        async def provider(reader, writer):
            try:
                header=await reader.readuntil(b"\r\n\r\n")
                length=next(int(line.split(b":",1)[1]) for line in header.split(b"\r\n") if line.lower().startswith(b"content-length:"))
                request=json.loads(await reader.readexactly(length))
                requests.append(request)
                actions=[
                    {"action":"click","mark":0,"progress":{"goal":"initial values","status":"complete","observation":"Value 17"}},
                    {"action":"click","mark":1,"progress":{"goal":"changed values","status":"complete","observation":"Value 26"}},
                ]
                if len(requests)<=2:
                    action=actions[len(requests)-1]
                else:
                    text=request["messages"][1]["content"][0]["text"]
                    answer="Initial 17; changed 26; restored" if "Value 17" in text and "Value 26" in text else "MISSING earlier phase"
                    action={"action":"done","result":answer,"progress":{"goal":"restore","status":"complete","observation":"Initial view visible"}}
                body=json.dumps({"id":"fake","object":"chat.completion","created":0,"model":"fake-visual",
                    "choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":json.dumps(action)}}],
                    "usage":{"prompt_tokens":17,"completion_tokens":5,"total_tokens":22}}).encode()
                writer.write(b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\nContent-Length: "+str(len(body)).encode()+b"\r\n\r\n"+body)
                await writer.drain()
            finally:
                writer.close()
                await writer.wait_closed()
        listener=await asyncio.start_server(provider,"127.0.0.1",0)
        port=listener.sockets[0].getsockname()[1]
        try:
            async with async_playwright() as pw:
                # Exercise the deployed Xvfb/Chromium renderer when requested;
                # this still draws and compares real canvas screenshots.
                import os
                browser=await pw.chromium.launch(headless=os.getenv("BROWSER_TEST_HEADFUL") != "1",args=["--no-sandbox"])
                try:
                    page=await browser.new_page()
                    await page.set_content("""<canvas id="report" width="250" height="90"></canvas>
                    <button onclick="draw(26)">Change view</button><button onclick="draw(17)">Restore view</button>
                    <script>function draw(v){let c=document.querySelector('canvas').getContext('2d');c.fillStyle='white';c.fillRect(0,0,250,90);c.fillStyle='black';c.font='30px sans-serif';c.fillText('Value '+v,20,50);}draw(17);</script>""")
                    operator=RemoteBrowserOperator(page)
                    self.assertNotIn("17",await operator.dom_snapshot())
                    ledger=UsageLedger("fake-task")
                    config=ModelConfig(f"http://127.0.0.1:{port}/v1","fake-key","fake-visual",True,
                                       ModelPolicy(first_max_tokens=300,max_tokens=700,timeout_seconds=9,reasoning_effort="none"))
                    planner=Planner(config,mode="vlm",usage=ledger)
                    async def emit(frame): frames.append(frame)
                    result=await build(operator,emit,planner=planner).ainvoke({"instruction":"Read initial and changed values, then restore","step":0,"history":[],"max_steps":8})
                    self.assertEqual(result["outcome"],"done")
                    self.assertIn("Initial 17; changed 26; restored",result["result"])
                    goals=result["task_memory"]["goals"]
                    shots=[f["data"] for f in frames if f["type"]=="screenshot"]
                    self.assertEqual(goals[0]["screenshot_sha256"],hashlib.sha256(shots[0].encode()).hexdigest())
                    self.assertEqual(goals[1]["screenshot_sha256"],hashlib.sha256(shots[1].encode()).hexdigest())
                    self.assertNotEqual(goals[0]["screenshot_sha256"],goals[1]["screenshot_sha256"])
                    self.assertEqual([r["max_tokens"] for r in requests],[300,700,700])
                    self.assertTrue(all(r["reasoning_effort"]=="none" for r in requests))
                    self.assertEqual(ledger.summary()["total_tokens"],66)
                    self.assertEqual(len([f for f in frames if f["type"]=="action"]),2)
                finally: await browser.close()
        finally:
            listener.close()
            await listener.wait_closed()
