"""DOM-first element location (a11y/role/text) to avoid expensive vision calls.

If an element can be located structurally we act without a screenshot; only when
this fails do we fall back to the VLM (use_vision=True). This is the core GUI
cost-reduction lever.
"""

from __future__ import annotations

from typing import Any

from playwright.async_api import Page


async def locate(page: Page, action: dict[str, Any]) -> bool:
    """Return True if the action's target can be located via the DOM/a11y tree."""
    sel = action.get("selector") or action.get("target")
    if not sel:
        return action.get("action") in ("navigate", "scroll", "read", "done")
    try:
        # role= prefix means accessibility-tree lookup, e.g. "role=button:Submit"
        if isinstance(sel, str) and sel.startswith("role="):
            spec = sel[len("role="):]
            role, _, name = spec.partition(":")
            loc = page.get_by_role(role, name=name) if name else page.get_by_role(role)
            return await loc.count() > 0
        return await page.locator(sel).count() > 0
    except Exception:
        return False
