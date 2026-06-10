"""Parse a model's free-form output into a structured action dict."""

from __future__ import annotations

import json
import re
from typing import Any

_VALID = {"navigate", "click", "type", "scroll", "read", "done", "call_user", "error"}


def parse_action(text: str) -> dict[str, Any]:
    """Extract an action JSON object from the model output.

    Accepts a bare JSON object or one embedded in ```json fences. Falls back to
    an error action when nothing parseable is found.
    """
    if not text:
        return {"action": "error", "message": "empty model output"}

    candidate = text.strip()
    fence = re.search(r"```(?:json)?\s*(\{.*?\})\s*```", candidate, re.DOTALL)
    if fence:
        candidate = fence.group(1)
    else:
        brace = re.search(r"\{.*\}", candidate, re.DOTALL)
        if brace:
            candidate = brace.group(0)

    try:
        obj = json.loads(candidate)
    except json.JSONDecodeError:
        return {"action": "error", "message": f"unparseable: {text[:120]}"}

    if obj.get("action") not in _VALID:
        return {"action": "error", "message": f"invalid action: {obj.get('action')}"}
    return obj


# ASCII URL charset only: CJK text/punctuation (，。、…) right after a URL in a
# Chinese instruction must NOT be swallowed into it (it punycodes into a bogus
# host and navigation DNS-fails). Trailing ASCII punctuation is stripped too.
_URL_RE = re.compile(r"https?://[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]+")
# bare domain like www.bilibili.com or example.com/path (no scheme)
_DOMAIN_RE = re.compile(r"((?:[a-z0-9-]+\.)+[a-z]{2,}(?:/[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]*)?)", re.I)

def _clean_url(u: str) -> str:
    return u.rstrip(".,;:!?'\")]}")

# common site aliases so "打开b站" works without a URL
_ALIASES = {
    "b站": "https://www.bilibili.com",
    "哔哩哔哩": "https://www.bilibili.com",
    "bilibili": "https://www.bilibili.com",
    "淘宝": "https://www.taobao.com",
    "百度": "https://www.baidu.com",
    "知乎": "https://www.zhihu.com",
    "微博": "https://weibo.com",
    "github": "https://github.com",
}


def extract_url(text: str) -> str:
    if not text:
        return ""
    if m := _URL_RE.search(text):
        return _clean_url(m.group(0))
    low = text.lower()
    for key, url in _ALIASES.items():
        if key.lower() in low:
            return url
    if d := _DOMAIN_RE.search(text):
        return "https://" + _clean_url(d.group(1))
    return ""
