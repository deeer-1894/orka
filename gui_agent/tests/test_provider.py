import asyncio
import importlib.util
import unittest
from types import SimpleNamespace
from unittest.mock import patch

from test_som_planner import FakeClient, response


class ProviderTests(unittest.IsolatedAsyncioTestCase):
    def components(self):
        self.assertIsNotNone(importlib.util.find_spec("agent.provider"), "usage-aware async provider is missing")
        from agent.config import ModelConfig
        from agent.provider import ModelClient, UsageLedger
        return ModelConfig, ModelClient, UsageLedger

    async def test_usage_is_reported_even_when_final_action_is_truncated(self):
        Config, _, Ledger = self.components()
        from agent.model import Planner
        frames = []
        async def emit(frame): frames.append(frame)
        ledger = Ledger("call", emit)
        reply = response('{"action":"click","mark":1}', reason="length")
        reply.usage = SimpleNamespace(prompt_tokens=31, completion_tokens=7, total_tokens=38)
        fake = FakeClient(reply)
        with patch("openai.AsyncOpenAI", return_value=fake) as sdk:
            result, _ = await Planner(Config("http://fake.test/v1", "fake-key", "selected-model", True), mode="vlm", usage=ledger).predict({"screenshot":"png"}, None)
        self.assertEqual(result["action"], "error")
        self.assertEqual(ledger.summary()["total_tokens"], 38)
        self.assertTrue(frames[0]["known"])
        self.assertEqual(frames[0]["model"], "selected-model")
        self.assertEqual(sdk.call_args.kwargs["api_key"], "fake-key")
        self.assertNotIn("fake-key", str(frames))

    async def test_uitars_cancels_request_and_records_unknown_usage(self):
        Config, _, Ledger = self.components()
        from agent.model import UITarsPlanner
        from test_uitars import b64_png
        ledger = Ledger("cancel")
        fake = FakeClient(wait=True)
        with patch("openai.AsyncOpenAI", return_value=fake), patch("openai.OpenAI", side_effect=AssertionError("sync SDK")):
            task = asyncio.create_task(UITarsPlanner(Config("http://fake.test/v1", "fake-key", "ui-model", True), usage=ledger).predict({"shots":[b64_png(100,100)]}))
            await asyncio.wait_for(fake.started.wait(), 1)
            task.cancel()
            with self.assertRaises(asyncio.CancelledError): await asyncio.wait_for(task, 1)
        self.assertTrue(fake.closed)
        self.assertEqual(ledger.summary()["unknown_calls"], 1)
        self.assertEqual(ledger.summary()["calls"], 1)

    async def test_missing_usage_is_unknown_and_config_repr_redacts_key(self):
        Config, Client, Ledger = self.components()
        cfg = Config("http://fake.test/v1", "fake-key", "chosen", True)
        self.assertNotIn("fake-key", repr(cfg))
        ledger = Ledger("missing")
        fake = FakeClient(response('{"action":"done","result":"done"}'))
        with patch("openai.AsyncOpenAI", return_value=fake):
            await Client(cfg, ledger).complete([], max_tokens=10)
        self.assertEqual(ledger.summary()["unknown_calls"], 1)
        self.assertEqual(ledger.summary()["known_calls"], 0)

    async def test_unverified_vision_rejected_before_provider(self):
        Config, _, _ = self.components()
        from agent.model import Planner
        with patch("openai.AsyncOpenAI") as sdk:
            result, _ = await Planner(Config("http://fake.test/v1", "fake-key", "text-model", False), mode="vlm").predict({"screenshot":"png"}, None)
        self.assertEqual(result["action"], "error")
        sdk.assert_not_called()


    async def test_real_async_sdk_reports_mock_http_provider_usage_for_both_planners(self):
        import json
        from agent.config import ModelConfig
        from agent.provider import UsageLedger
        from agent.model import Planner
        from test_uitars import b64_png
        requests = []
        async def serve(reader, writer):
            header = await reader.readuntil(b"\r\n\r\n")
            length = next(int(line.split(b":", 1)[1]) for line in header.split(b"\r\n") if line.lower().startswith(b"content-length:"))
            body = json.loads(await reader.readexactly(length))
            requests.append(body)
            content = "Action: finished(content='done')" if body["model"] == "chosen-ui" else '{"action":"done","result":"done"}'
            reply = json.dumps({"id":"mock", "object":"chat.completion", "created":0, "model":body["model"],
                "choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":content}}],
                "usage":{"prompt_tokens":17,"completion_tokens":5,"total_tokens":22}}).encode()
            writer.write(b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\nContent-Length: " + str(len(reply)).encode() + b"\r\n\r\n" + reply)
            await writer.drain()
            writer.close()
            await writer.wait_closed()
        listener = await asyncio.start_server(serve, "127.0.0.1", 0)
        port = listener.sockets[0].getsockname()[1]
        try:
            for mode, model in [("vlm", "chosen-som"), ("uitars", "chosen-ui")]:
                ledger = UsageLedger(mode)
                planner = Planner(ModelConfig(f"http://127.0.0.1:{port}/v1", "fake-account-key", model, True), mode=mode, usage=ledger)
                result, _ = await planner.predict({"instruction":"read", "screenshot":b64_png(100,100)}, None)
                self.assertEqual(result["action"], "done")
                self.assertEqual(ledger.summary()["total_tokens"], 22)
                self.assertEqual(ledger.summary()["calls"], 1)
                self.assertEqual(ledger.summary()["unknown_calls"], 0)
            self.assertEqual([request["model"] for request in requests], ["chosen-som", "chosen-ui"])
            self.assertNotIn("fake-account-key", json.dumps(requests))
        finally:
            listener.close()
            await listener.wait_closed()


class PolicyTests(unittest.IsolatedAsyncioTestCase):
    async def test_explicit_policy_reaches_both_planners_and_bounds_first_and_later_calls(self):
        from agent.config import ModelConfig
        from agent.model import Planner
        from test_uitars import b64_png
        for mode in ("vlm","uitars"):
            for model in ("glm-5.3-flash","arbitrary-model"):
                with self.subTest(mode=mode,model=model):
                    config=ModelConfig.from_wire({"base_url":"http://fake.test/v1","api_key":"fake-key","model":model,"vision_verified":True,
                        "policy":{"first_max_tokens":300,"max_tokens":700,"timeout_seconds":9,"reasoning_effort":"none"}})
                    planner=Planner(config,mode=mode)
                    reply=response("Action: finished(content='done')" if mode=="uitars" else '{"action":"done","result":"done"}')
                    fake=FakeClient(reply)
                    with patch("openai.AsyncOpenAI",return_value=fake) as sdk:
                        await planner.predict({"screenshot":b64_png(30,30)},None)
                        self.assertEqual(fake.request["max_tokens"],300)
                        self.assertEqual(fake.request["reasoning_effort"],"none")
                        self.assertEqual(sdk.call_args.kwargs["timeout"],9)
                        await planner.predict({"screenshot":b64_png(30,30)},None)
                        self.assertEqual(fake.request["max_tokens"],700)

    async def test_policy_empty_does_not_guess_reasoning_and_large_policy_cannot_exceed_gui_cap(self):
        from agent.config import ModelConfig
        from agent.model import Planner
        for policy in ({},{"max_tokens":8192,"timeout_seconds":300}):
            config=ModelConfig.from_wire({"base_url":"http://fake.test/v1","api_key":"fake-key","model":"glm-5.3-flash","vision_verified":True,"policy":policy})
            fake=FakeClient(response('{"action":"done","result":"done"}'))
            with patch("openai.AsyncOpenAI",return_value=fake) as sdk:
                await Planner(config,mode="vlm").predict({"screenshot":"png"},None)
            self.assertNotIn("reasoning_effort",fake.request)
            self.assertEqual(fake.request["max_tokens"],4096)
            self.assertEqual(sdk.call_args.kwargs["timeout"],45)

    async def test_invalid_policy_is_rejected_before_provider(self):
        from agent.config import ModelConfig
        for policy in ({"max_tokens":-1},{"first_max_tokens":True},{"timeout_seconds":"9"},{"reasoning_effort":"invented"}):
            with self.subTest(policy=policy), patch("openai.AsyncOpenAI") as sdk:
                with self.assertRaises(ValueError):
                    ModelConfig.from_wire({"base_url":"http://fake.test/v1","api_key":"fake-key","model":"m","vision_verified":True,"policy":policy})
                sdk.assert_not_called()
