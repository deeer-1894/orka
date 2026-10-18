"""The visual planner must stay cancellable and execute only final actions."""
import asyncio
import os
import unittest
from types import SimpleNamespace
from unittest.mock import patch
from agent.model import Planner
from agent.config import ModelConfig, ModelPolicy


class FakeClient:
    def __init__(self, response=None, wait=False):
        self.response = response
        self.wait = wait
        self.started = asyncio.Event()
        self.request = None
        self.closed = False
        self.chat = SimpleNamespace(completions=self)

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        self.closed = True

    async def create(self, **kwargs):
        self.request = kwargs
        self.started.set()
        if self.wait:
            await asyncio.Event().wait()
        return self.response


def response(content, reason='stop', thinking=None):
    return SimpleNamespace(choices=[SimpleNamespace(finish_reason=reason, message=SimpleNamespace(content=content, reasoning_content=thinking))])


class SomPlannerTests(unittest.IsolatedAsyncioTestCase):
    def planner(self, model='glm-5.3-flash', effort=''):
        return Planner(ModelConfig("https://example.test/v1", "fixture", model, True, ModelPolicy(reasoning_effort=effort)), mode="vlm")

    def sdk(self, fake):
        return patch('openai.AsyncOpenAI', return_value=fake)

    async def test_vision_input_and_known_reasoning_policy(self):
        fake = FakeClient(response('{"action":"navigate","url":"https://example.test"}'))
        with self.sdk(fake), patch('openai.OpenAI', side_effect=AssertionError('blocking client used')):
            result, vision = await self.planner(effort='low').predict({'instruction':'open site','screenshot':'cG5n'},None)
        self.assertTrue(vision)
        self.assertEqual(result['url'],'https://example.test')
        self.assertEqual(fake.request['reasoning_effort'],'low')
        self.assertEqual(fake.request['messages'][1]['content'][1]['type'],'image_url')
        self.assertTrue(fake.closed)

    async def test_waiting_request_is_cancellable(self):
        fake = FakeClient(wait=True)
        with self.sdk(fake), patch('openai.OpenAI', side_effect=AssertionError('blocking client used')):
            task=asyncio.create_task(self.planner().predict({'instruction':'open'},None))
            await asyncio.wait_for(fake.started.wait(),1)
            task.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await asyncio.wait_for(task,1)
        self.assertTrue(fake.closed)

    async def test_unknown_models_keep_provider_reasoning_defaults(self):
        fake=FakeClient(response('{"action":"done","result":"observed"}'))
        with self.sdk(fake), patch('openai.OpenAI', side_effect=AssertionError('blocking client used')):
            await self.planner('unknown-vlm').predict({'instruction':'read'},None)
        self.assertNotIn('reasoning_effort',fake.request)

    async def test_truncation_or_thinking_cannot_become_executable_action(self):
        for reply in [response('{"action":"click","mark":2}','length'),response(None,thinking='{"action":"click","mark":2}')]:
            fake=FakeClient(reply)
            with self.sdk(fake), patch('openai.OpenAI', side_effect=AssertionError('blocking client used')):
                result,_=await self.planner().predict({'instruction':'click'},None)
            self.assertEqual(result['action'],'error')
