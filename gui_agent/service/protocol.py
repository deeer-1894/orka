"""Validate trusted service requests before queueing or touching a browser."""
from dataclasses import dataclass
import math
from agent.config import ModelConfig


@dataclass(frozen=True)
class RunRequest:
    session_id: str
    owner_id: str
    conversation_id: str
    run_id: str
    instruction: str
    model_config: ModelConfig
    max_steps: int
    queue_timeout: float
    execution_timeout: float

    @classmethod
    def parse(cls, message):
        if not isinstance(message, dict):
            raise ValueError("invalid run request")
        identity = message.get("identity")
        if not isinstance(identity, dict):
            raise ValueError("trusted identity is required")
        ids = [identity.get(key) for key in ("owner_id", "conversation_id", "run_id")]
        ids.append(message.get("session_id"))
        if any(not isinstance(value, str) or not value.strip() or len(value) > 256 for value in ids):
            raise ValueError("owner, conversation, run and invocation IDs are required")
        instruction = message.get("instruction")
        if not isinstance(instruction, str) or not instruction.strip() or len(instruction) > 32000:
            raise ValueError("instruction must be nonempty text, at most 32000 characters")
        config = ModelConfig.from_wire(message.get("model_config"))
        steps = message.get("max_steps", 10)
        if type(steps) is not int or not 1 <= steps <= 100:
            raise ValueError("max_steps must be between 1 and 100")
        def seconds(key, default):
            value = message.get(key, default)
            if type(value) not in (int, float) or not math.isfinite(value) or not 0 < value <= 600:
                raise ValueError("queue/execution timeout must be positive and at most 600 seconds")
            return value
        return cls(ids[3], *ids[:3], instruction, config, steps,
                   seconds("queue_timeout", 30), seconds("execution_timeout", 90))
