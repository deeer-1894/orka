"""One cancellable provider adapter and actual, incremental usage accounting."""
from __future__ import annotations

from agent.config import ModelConfig


class UsageLedger:
    def __init__(self, invocation_id: str, emit=None):
        self.invocation_id = invocation_id
        self.emit = emit
        self.calls = 0
        self.unknown_calls = 0
        self.totals = {key: 0 for key in ("prompt_tokens", "completion_tokens", "total_tokens")}

    async def record(self, model, usage):
        self.calls += 1
        values = {}
        for key in self.totals:
            value = getattr(usage, key, None)
            values[key] = value if type(value) is int and value >= 0 else None
            if values[key] is not None:
                self.totals[key] += value
        known = all(value is not None for value in values.values())
        self.unknown_calls += int(not known)
        frame = {"type": "usage", "call_id": f"{self.invocation_id}:{self.calls}",
                 "model": model, "known": known, **values}
        if self.emit:
            await self.emit(frame)

    def summary(self):
        return {"calls": self.calls, "known_calls": self.calls - self.unknown_calls,
                "unknown_calls": self.unknown_calls, **self.totals}


class ModelClient:
    def __init__(self, config: ModelConfig, usage: UsageLedger):
        self.config = config
        self.usage = usage
        self.calls = 0

    async def complete(self, messages, **options):
        from openai import AsyncOpenAI
        reported = None
        policy = self.config.policy
        cap = min(options.pop("max_tokens", 4096), 4096)
        selected = policy.first_max_tokens if self.calls == 0 and policy.first_max_tokens else policy.max_tokens
        if selected: cap = min(cap, selected)
        options["max_tokens"] = cap
        effort = policy.reasoning_effort
        if effort: options["reasoning_effort"] = effort
        timeout = min(options.pop("timeout", 45), 45)
        if policy.timeout_seconds: timeout = min(timeout, policy.timeout_seconds)
        self.calls += 1
        try:
            async with AsyncOpenAI(base_url=self.config.base_url,
                                   api_key=self.config.api_key or "empty",
                                   timeout=timeout, max_retries=0) as client:
                response = await client.chat.completions.create(
                    model=self.config.model, messages=messages, **options)
                reported = getattr(response, "usage", None)
                return response
        finally:
            # A cancelled/failed exchange without provider usage is unknown,
            # not a fabricated zero. Count before interpreting the model output.
            await self.usage.record(self.config.model, reported)
