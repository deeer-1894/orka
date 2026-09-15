"""Offline graph contract tests: execution receipts, observations and privacy."""
import json
import unittest
from types import SimpleNamespace
from unittest.mock import MagicMock
from service.browser_runtime import BrowserRuntime
from unittest.mock import AsyncMock, patch

from agent.graph import build
from agent.model import Planner
from agent.config import ModelConfig
from test_som_planner import FakeClient, response


class EvidenceTests(unittest.IsolatedAsyncioTestCase):
    async def run_graph(self, actions, max_steps=50, sensitive=False, fail=False):
        states, frames = [], []
        actions = iter(actions)
        async def predict(state, page):
            states.append(dict(state))
            return next(actions), False
        async def execute(action):
            if fail:
                raise RuntimeError("execution failed")
            return "operator returned"
        op = SimpleNamespace(
            page=SimpleNamespace(url="https://example.test", title=AsyncMock(return_value="Form")),
            screenshot=AsyncMock(return_value="cG5n"),
            dom_snapshot=AsyncMock(return_value="Observed form state"),
            title=AsyncMock(return_value="Form"), execute=execute,
            input_is_sensitive=AsyncMock(return_value=sensitive),
        )
        async def emit(frame):
            frames.append(frame)
        planner = SimpleNamespace(mode="rule", predict=predict)
        with patch("agent.graph.Planner", return_value=planner):
            result = await build(op, emit).ainvoke(
                {"history": [], "step": 0, "max_steps": max_steps, "status": "running"},
                config={"recursion_limit": 200},
            )
        return result, states, frames

    async def test_executed_input_values_and_observations_reach_planner_and_stream(self):
        result, states, frames = await self.run_graph([
            {"action": "type", "selector": "#amount", "text": "0"},
            {"action": "type", "selector": "#amount", "text": "15"},
            {"action": "done", "result": "model claim"},
        ])
        evidence = states[-1].get("evidence", [])
        self.assertEqual([e["text"] for e in evidence if e.get("action") == "type"], ["0", "15"])
        self.assertEqual([e["kind"] for e in evidence], ["observation", "action", "observation", "action", "observation"])
        self.assertEqual([e["seq"] for e in evidence], list(range(1, 6)))
        self.assertIn("Observed form state", json.dumps(evidence))
        self.assertEqual([f["evidence"]["text"] for f in frames if f["type"] == "action"], ["0", "15"])
        self.assertEqual(result.get("outcome"), "done")

    async def test_sensitive_input_is_absent_from_new_evidence(self):
        result, states, frames = await self.run_graph([
            {"action": "type", "selector": "#password", "text": "s3cr3t", "_thought": "s3cr3t"},
            {"action": "done", "result": "done"},
        ], sensitive=True)
        evidence = result.get("evidence", [])
        self.assertTrue(any(e.get("input_redacted") for e in evidence))
        self.assertNotIn("s3cr3t", json.dumps(evidence))
        self.assertNotIn("s3cr3t", json.dumps([f.get("evidence") for f in frames]))

    async def test_failed_execution_is_not_a_completed_action(self):
        result, _, frames = await self.run_graph([{"action": "click", "selector": "#button"}], fail=True)
        self.assertEqual(result["status"], "ERROR")
        self.assertFalse(any(f["type"] == "action" for f in frames))

    async def test_budget_stop_is_partial_and_keeps_last_action(self):
        result, _, _ = await self.run_graph([{"action": "type", "selector": "#amount", "text": "15"}], max_steps=1)
        self.assertEqual(result.get("outcome"), "partial")
        self.assertTrue(any(e.get("text") == "15" for e in result.get("evidence", [])))

    async def test_sequence_is_bounded_and_keeps_absolute_order(self):
        actions = [{"action": "type", "selector": "#amount", "text": str(i)} for i in range(30)]
        actions.append({"action": "done", "result": "done"})
        result, _, _ = await self.run_graph(actions)
        evidence = result.get("evidence", [])
        self.assertGreater(len(evidence), 0)
        self.assertLessEqual(len(evidence), 24)
        self.assertGreater(evidence[0]["seq"], 1)
        self.assertEqual([e["text"] for e in evidence if e.get("action") == "type"][-1], "29")

    async def test_som_prompt_uses_receipts_not_raw_actions(self):
        fake = FakeClient(response('{"action":"done","result":"done"}'))
        evidence = [{"seq": 1, "kind": "action", "action": "type", "text": "15", "result": "operator returned"},
                    {"seq": 2, "kind": "observation", "content": "Observed amount 15"}]
        with patch("openai.AsyncOpenAI", return_value=fake):
            await Planner(ModelConfig("http://fake.test/v1", "fake-key", "test-model", False), mode="llm")._som_predict({"evidence": evidence, "history": [{"action": {"action": "type", "text": "private-raw"}}]})
        content = fake.request["messages"][1]["content"]
        self.assertIn('"text": "15"', content)
        self.assertIn("Observed amount 15", content)
        self.assertNotIn("private-raw", content)

    async def test_repeated_identical_actions_stop_partial_with_receipts(self):
        action = {"action": "click", "selector": "#button"}
        result, _, _ = await self.run_graph([action, action, action])
        self.assertEqual(result.get("outcome"), "partial")
        self.assertEqual(len([e for e in result["evidence"] if e["kind"] == "action"]), 3)

    async def test_uitars_receives_actual_receipts_on_latest_observation(self):
        from agent.model import UITarsPlanner
        from test_uitars import b64_png
        planner = UITarsPlanner(ModelConfig("http://fake.test/v1", "fake-key", "test-model", True))
        with patch.object(planner, "_chat", return_value="Action: finished(content='done')") as chat:
            await planner.predict({"shots": [b64_png(100, 100)], "evidence": [
                {"kind": "action", "action": "type", "text": "15"}]})
        latest = chat.call_args.args[0][-1]["content"]
        self.assertIn('"text": "15"', latest[-1]["text"])

    async def test_ws_preserves_partial_outcome_without_macro_replay(self):
        from service.web_socket import server
        from service.runtime import ExecutionQueue
        from starlette.websockets import WebSocketState
        ws = SimpleNamespace(client_state=WebSocketState.CONNECTED, send_json=AsyncMock())
        pool = SimpleNamespace(get=AsyncMock(return_value=(SimpleNamespace(page=MagicMock(close=AsyncMock())), True)))
        graph = SimpleNamespace(ainvoke=AsyncMock(return_value={
            "status": "END", "outcome": "partial", "result": "budget exhausted",
        }))
        with patch.object(server, "_runtime", BrowserRuntime(pool,ExecutionQueue())), \
             patch.object(server, "build", return_value=graph), patch.dict("os.environ", {"GUI_PLANNER":"rule", "MACRO_ENABLE":"1"}):
            await server.run_task(ws, {"instruction": "offline", "max_steps": 1,
                "session_id":"call", "identity":{"owner_id":"a", "conversation_id":"c", "run_id":"r"},
                "model_config":{"base_url":"http://fake.test/v1", "api_key":"fake-key", "model":"chosen", "vision_verified":True}})
        terminal = ws.send_json.call_args.args[0]
        self.assertEqual(terminal["type"], "done")
        self.assertEqual(terminal["outcome"], "partial")
        graph.ainvoke.assert_awaited_once()


class BrowserPrivacyTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        from playwright.async_api import async_playwright
        from operators.remote_browser import RemoteBrowserOperator
        self.pw = await async_playwright().start()
        self.browser = await self.pw.chromium.launch(headless=True, args=["--no-sandbox"])
        self.page = await self.browser.new_page()
        self.op = RemoteBrowserOperator()
        self.op._page = self.page
        await self.page.set_content("""<main>Form</main><input id="amount" type="number" value="0">
            <input id="password" type="password" value="existing-secret">
            <input id="token" autocomplete="one-time-code"><input id="private" data-sensitive>
            <textarea id="memo"></textarea>""")

    async def asyncTearDown(self):
        await self.browser.close()
        await self.pw.stop()

    async def test_target_classification_for_handles_selectors_and_keyboard_focus(self):
        for selector in ["#password", "#token", "#private"]:
            self.assertTrue(await self.op.input_is_sensitive({"selector": selector}))
            handle = await self.page.query_selector(selector)
            self.assertTrue(await self.op.input_is_sensitive({"_handle": handle}))
            await handle.focus()
            self.assertTrue(await self.op.input_is_sensitive({}))
        self.assertFalse(await self.op.input_is_sensitive({"selector": "#amount"}))
        self.assertFalse(await self.op.input_is_sensitive({"selector": "#memo"}))
        await self.page.locator("#amount").focus()
        self.assertFalse(await self.op.input_is_sensitive({}))
        self.assertTrue(await self.op.input_is_sensitive({"selector": "[invalid"}))

    async def test_marks_hide_existing_password_but_keep_live_numeric_values(self):
        from operators.marks import collect_marks, marks_text, mark_action_to_dict
        marks = await collect_marks(self.page)
        text = marks_text(marks)
        self.assertNotIn("existing-secret", text)
        self.assertIn("[redacted]", text)
        self.assertIn("0", text)
        private_mark = [m for m in marks if await m.handle.get_attribute("id") == "password"][0]
        action = mark_action_to_dict({"action": "type", "mark": private_mark.index, "text": "next-secret"}, marks)
        from agent.evidence import Evidence
        evidence = Evidence()
        receipt = await evidence.prepare(self.op, action)
        result = await self.op.execute(action)
        evidence.executed(1, receipt, result)
        evidence.observe(1, "echo next-secret")
        self.assertNotIn("next-secret", json.dumps(evidence.events))
        self.assertNotIn("existing-secret", json.dumps(evidence.events))
        self.assertEqual(await private_mark.handle.input_value(), "next-secret")

    async def test_keyboard_receipt_distinguishes_append_and_submit_from_fill(self):
        from agent.evidence import Evidence
        evidence = Evidence()
        await self.page.locator("#memo").focus()
        action = {"action": "type", "text": "hello\n\n"}
        receipt = await evidence.prepare(self.op, action)
        result = await self.op.execute(action)
        receipt = evidence.executed(1, receipt, result)
        self.assertEqual(receipt.get("input_mode"), "keyboard")
        self.assertEqual(receipt.get("text"), "hello")
        self.assertTrue(receipt.get("submit"))
        self.assertEqual(await self.page.locator("#memo").input_value(), "hello\n")

    async def test_long_fields_are_bounded_and_private_metadata_never_serialized(self):
        from agent.evidence import Evidence
        evidence = Evidence()
        action = {"action": "type", "selector": "#memo", "text": "界" * 10000, "_handle": await self.page.query_selector("#memo"), "_thought": "hidden-thought"}
        receipt = await evidence.prepare(self.op, action)
        evidence.executed(1, receipt, "x" * 10000)
        evidence.observe(1, "y" * 10000)
        serialized = json.dumps(evidence.events, ensure_ascii=False)
        self.assertLess(len(serialized), 2500)
        self.assertNotIn("hidden-thought", serialized)
        self.assertNotIn("_handle", serialized)
        self.assertIn("truncated", serialized)


    async def test_real_mark_receipt_separates_old_label_from_executed_value(self):
        from operators.marks import collect_marks, mark_action_to_dict
        from agent.evidence import Evidence
        marks = await collect_marks(self.page)
        mark = [m for m in marks if await m.handle.get_attribute("id") == "amount"][0]
        action = mark_action_to_dict({"action": "type", "mark": mark.index, "text": "15"}, marks)
        evidence = Evidence()
        receipt = await evidence.prepare(self.op, action)
        result = await self.op.execute(action)
        receipt = evidence.executed(1, receipt, result)
        self.assertIn("0", receipt["target"])
        self.assertEqual(receipt["text"], "15")
        self.assertEqual(receipt["input_mode"], "fill")
        self.assertEqual(await mark.handle.input_value(), "15")

    async def test_unknown_target_and_explicit_sensitive_input_fail_closed(self):
        from agent.evidence import Evidence
        evidence = Evidence()
        op = SimpleNamespace(input_is_sensitive=AsyncMock(side_effect=RuntimeError("unavailable")))
        receipt = await evidence.prepare(op, {"action": "type", "text": "private-value\n"})
        evidence.executed(1, receipt, "echo private-value")
        evidence.observe(1, "echo private-value")
        self.assertNotIn("private-value", json.dumps(evidence.events))
        receipt = await evidence.prepare(self.op, {"action": "type", "selector": "#memo", "text": "private-value", "sensitive": True})
        self.assertTrue(receipt["input_redacted"])


    async def test_receipt_excludes_parameters_the_operator_did_not_execute(self):
        from agent.evidence import Evidence
        evidence = Evidence()
        receipt = await evidence.prepare(self.op, {"action": "click", "selector": "#amount", "text": "unused-secret", "url": "unused-url"})
        self.assertNotIn("unused-secret", json.dumps(receipt))
        self.assertEqual(receipt["target"], "#amount")
