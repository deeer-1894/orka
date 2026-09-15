"""Explicit per-run model configuration; never reads environment credentials."""
from dataclasses import dataclass, field
from urllib.parse import urlsplit


@dataclass(frozen=True)
class ModelPolicy:
    first_max_tokens: int = 0
    max_tokens: int = 0
    timeout_seconds: int = 0
    reasoning_effort: str = ""

    @classmethod
    def from_wire(cls, value):
        if value is None: return cls()
        if not isinstance(value, dict): raise ValueError("invalid model policy")
        fields = ("first_max_tokens", "max_tokens", "timeout_seconds")
        if any(type(value.get(k, 0)) is not int or value.get(k, 0) < 0 for k in fields):
            raise ValueError("invalid model policy limits")
        effort = value.get("reasoning_effort", "")
        if not isinstance(effort, str) or effort not in ("", "none", "minimal", "low", "medium", "high", "xhigh"):
            raise ValueError("invalid reasoning_effort")
        if value.get("max_tokens", 0) and value.get("first_max_tokens", 0) > value["max_tokens"]:
            raise ValueError("first_max_tokens exceeds max_tokens")
        if any(value.get(k, 0) > limit for k, limit in (("first_max_tokens",1048576),("max_tokens",1048576),("timeout_seconds",7200))):
            raise ValueError("model policy limit is too large")
        return cls(*(value.get(k, 0) for k in fields), effort)

@dataclass(frozen=True)
class ModelConfig:
    base_url: str
    api_key: str = field(repr=False)
    model: str
    vision_verified: bool
    policy: ModelPolicy = field(default_factory=ModelPolicy)

    @classmethod
    def from_wire(cls, value):
        if not isinstance(value, dict):
            raise ValueError("model_config is required")
        if any(not isinstance(value.get(k), str) for k in ("base_url", "api_key", "model")) or type(value.get("vision_verified")) is not bool:
            raise ValueError("invalid model_config fields")
        if len(value["base_url"]) > 4096 or len(value["api_key"]) > 8192 or len(value["model"]) > 256:
            raise ValueError("model_config field is too long")
        config = cls(value["base_url"], value["api_key"], value["model"], value["vision_verified"], ModelPolicy.from_wire(value.get("policy")))
        config.validate("llm")
        return config

    def validate(self, mode):
        url = urlsplit(self.base_url)
        if url.scheme not in ("http", "https") or not url.hostname or url.username or url.password or url.query or url.fragment or not self.model.strip():
            raise ValueError("model_config requires a model and HTTP(S) provider base URL without credentials/query")
        if mode not in ("vlm", "llm", "uitars", "rule"):
            raise ValueError("unsupported GUI_PLANNER mode")
        if mode in ("vlm", "uitars") and not self.vision_verified:
            raise ValueError("selected model has no verified vision capability")
