"""FastAPI WebSocket service for the GUI executor.

Protocol (control layer <-> gui_agent):
  in:  {"type":"run","instruction":..,"session_id":..,"max_steps":..,"cdp_url":..}
  out: {"type":"screenshot","data":b64,...} | {"type":"observe","mode":..} |
       {"type":"action","action":..,"target":..,"result":..} |
       {"type":"call_user","reason":..} | {"type":"done","summary":..} |
       {"type":"error","error":..}
"""

from __future__ import annotations

import asyncio
import os
from contextlib import asynccontextmanager
from typing import Any

from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from starlette.websockets import WebSocketState

from agent.graph import build
from agent.macro import MacroStore
from operators.remote_browser import RemoteBrowserOperator


@asynccontextmanager
async def lifespan(_app: "FastAPI"):
    # Launch the shared browser on the app's main loop so it persists across
    # requests (a browser created inside a request task dies when the task ends).
    try:
        await get_operator(os.getenv("CDP_URL", ""))
    except Exception:
        pass
    yield
    await reset_operator()


app = FastAPI(title="orka-gui-agent", lifespan=lifespan)

_macros = MacroStore(os.getenv("MACRO_STORE", "./data/gui_macros.json"))
_MACRO_ENABLED = os.getenv("MACRO_ENABLE", "1") == "1"

# One persistent browser shared across runs: keeps the live noVNC view alive and
# lets follow-up tasks continue in the same session. Runs are serialized.
_op_lock = asyncio.Lock()
_operator: RemoteBrowserOperator | None = None


async def get_operator(cdp_url: str) -> RemoteBrowserOperator:
    global _operator
    if _operator is None:
        op = RemoteBrowserOperator(cdp_url=cdp_url)
        await op.start()
        _operator = op
    return _operator


async def reset_operator() -> None:
    global _operator
    if _operator is not None:
        try:
            await _operator.close()
        except Exception:
            pass
        _operator = None


@app.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok"}


async def run_task(ws: WebSocket, msg: dict[str, Any]) -> None:
    session_id = msg.get("session_id", "")
    instruction = msg.get("instruction", "")
    max_steps = int(msg.get("max_steps", 8))
    cdp_url = msg.get("cdp_url") or os.getenv("CDP_URL", "")

    async def emit(frame: dict[str, Any]) -> None:
        # close-safe: the control layer closes the socket right after `done`;
        # never raise (and never cascade) on send-after-close.
        if ws.client_state != WebSocketState.CONNECTED:
            return
        try:
            await ws.send_json(frame)
        except (RuntimeError, WebSocketDisconnect):
            pass

    async with _op_lock:
        try:
            operator = await get_operator(cdp_url)
        except Exception as e:  # noqa: BLE001
            await emit({"type": "error", "error": f"browser start failed: {e}", "session_id": session_id})
            return

        # Start each task from a blank page so a stale page left by a previous,
        # unrelated task doesn't confuse the planner into a click loop.
        try:
            await operator.page.goto("about:blank")
        except Exception:  # noqa: BLE001
            pass

        try:
            # Fast path: replay a recorded macro deterministically (no predict/VLM).
            if _MACRO_ENABLED and await replay_macro(operator, emit, instruction, session_id):
                return

            graph = build(operator, emit)
            init = {
                "instruction": instruction,
                "session_id": session_id,
                "step": 0,
                "max_steps": max_steps,
                "history": [],
                "status": "running",
                "use_vision": os.getenv("VLM_ENABLE") == "1",
            }
            final = await graph.ainvoke(init, config={"recursion_limit": max_steps * 3 + 6})
            status = final.get("status", "END")
            if status == "CALL_USER":
                await emit({"type": "call_user", "reason": final.get("call_user", ""), "session_id": session_id})
            elif status == "ERROR":
                await emit({"type": "error", "error": final.get("error", "error"), "session_id": session_id})
            else:
                if _MACRO_ENABLED:
                    actions = [h["action"] for h in final.get("history", []) if h.get("action")]
                    if _macro_replayable(actions):
                        _macros.put(instruction, [_macro_clean(a) for a in actions])
                await emit({"type": "done", "summary": final.get("result", ""), "session_id": session_id})
        except Exception as e:  # noqa: BLE001
            # On failure, drop the shared browser so the next run gets a fresh one.
            await reset_operator()
            await emit({"type": "error", "error": str(e), "session_id": session_id})
        # Note: the browser is intentionally kept alive across runs so the live
        # noVNC view persists and follow-up tasks continue in the same session.


# Macros must replay deterministically. Coordinate- or handle-addressed steps
# (UI-TARS clicks, resolved Set-of-Marks handles) break on any layout drift, so
# only URL/selector-addressed sequences are cached.
_MACRO_SAFE_KINDS = {"navigate", "navigate_back", "scroll", "wait", "read"}


def _macro_replayable(actions: list[dict[str, Any]]) -> bool:
    if not actions:
        return False
    for a in actions:
        kind = a.get("action")
        if kind in _MACRO_SAFE_KINDS and "x" not in a:
            continue
        if kind in ("click", "type") and a.get("selector") and "x" not in a and "_handle" not in a:
            continue
        return False
    return True


def _macro_clean(action: dict[str, Any]) -> dict[str, Any]:
    """Strip private/transient keys (thoughts, handles) before persisting."""
    return {k: v for k, v in action.items() if not k.startswith("_")}


async def replay_macro(operator, emit, instruction, session_id) -> bool:
    """Replay a recorded macro. Returns True if it handled the task; on any
    failure it returns False so the caller falls back to the explore loop."""
    actions = _macros.get(instruction)
    if not actions:
        return False
    await emit({"type": "observe", "mode": "macro", "tokens": 0, "session_id": session_id})
    try:
        for action in actions:
            result = await operator.execute(action)
            await emit({
                "type": "action",
                "action": action.get("action"),
                "target": action.get("url") or action.get("selector") or "",
                "result": result,
                "session_id": session_id,
            })
        await emit({"type": "done", "summary": await operator.dom_snapshot(), "session_id": session_id})
        return True
    except Exception:  # noqa: BLE001
        return False  # fall back to exploration


# Auth: when GUI_AUTH_TOKEN is set, every WS connection must present it as
# `Authorization: Bearer <token>` (or `?token=`). The GUI executor drives a real
# browser, so an unauthenticated port is a strong SSRF / internal-probe surface.
_GUI_AUTH_TOKEN = os.getenv("GUI_AUTH_TOKEN", "")
# Origin allowlist blocks browser-based cross-site WS hijacking; server-to-server
# clients send no Origin header and are allowed.
_ALLOWED_ORIGIN_HOSTS = {"localhost", "127.0.0.1", "[::1]"}


def _authorized(ws: WebSocket) -> bool:
    # Origin check (only meaningful for browser clients that set it).
    origin = ws.headers.get("origin")
    if origin:
        from urllib.parse import urlparse

        host = urlparse(origin).hostname or ""
        if host not in _ALLOWED_ORIGIN_HOSTS:
            return False
    if not _GUI_AUTH_TOKEN:
        return True  # dev: no token configured
    auth = ws.headers.get("authorization", "")
    presented = auth[7:] if auth.lower().startswith("bearer ") else ws.query_params.get("token", "")
    import hmac  # constant-time compare

    return hmac.compare_digest(presented, _GUI_AUTH_TOKEN)


@app.websocket("/api/v1/exec/gui/ws")
async def gui_ws(ws: WebSocket) -> None:
    if not _authorized(ws):
        await ws.close(code=1008)  # policy violation
        return
    await ws.accept()
    try:
        while True:
            msg = await ws.receive_json()
            if msg.get("type") == "run":
                # Run as a cancellable task and watch for the client closing the
                # socket (i.e. /chat/kill on the control side). On disconnect we
                # cancel the run so it stops driving the browser and RELEASES the
                # shared _op_lock — otherwise a runaway run would block every
                # other conversation's browser task.
                runner = asyncio.create_task(run_task(ws, msg))
                while not runner.done():
                    try:
                        ev = await asyncio.wait_for(ws.receive(), timeout=0.5)
                    except asyncio.TimeoutError:
                        continue
                    except (WebSocketDisconnect, RuntimeError):
                        runner.cancel()
                        break
                    if ev.get("type") == "websocket.disconnect":
                        runner.cancel()
                        break
                try:
                    await runner
                except asyncio.CancelledError:
                    pass
                if ws.client_state != WebSocketState.CONNECTED:
                    return
            else:
                await ws.send_json({"type": "error", "error": f"unknown message type {msg.get('type')}"})
    except WebSocketDisconnect:
        return
