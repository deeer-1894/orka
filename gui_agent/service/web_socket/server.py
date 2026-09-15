"""Authenticated GUI transport over isolated in-memory browser sessions."""
from __future__ import annotations

import asyncio
import hmac
import ipaddress
import os
from contextlib import asynccontextmanager

from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from starlette.websockets import WebSocketState

from agent.graph import build
from agent.model import Planner
from agent.provider import UsageLedger
from operators.sessions import SessionPool
from service.protocol import RunRequest
from service.runtime import ExecutionQueue, QueueFull
from service.browser_runtime import BrowserRuntime
from service.browser_bridge import serve_bridge

_pool = SessionPool(cdp_url=os.getenv("CDP_URL", ""))
_queue = ExecutionQueue(max_waiters=8)
_runtime = BrowserRuntime(_pool, _queue)
_GUI_AUTH_TOKEN = os.getenv("GUI_AUTH_TOKEN", "")
_PRIVATE_NETWORKS = tuple(ipaddress.ip_network(net) for net in (
    "127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7"))


@asynccontextmanager
async def lifespan(_app):
    yield
    await _pool.close()


app = FastAPI(title="orka-gui-agent", lifespan=lifespan)


@app.get("/health")
async def health():
    return {"status": "ok"}


def _authorized(ws: WebSocket) -> bool:
    if not _GUI_AUTH_TOKEN or ws.headers.get("origin"):
        return False
    try:
        peer = ipaddress.ip_address(ws.client.host)
        if not any(peer in network for network in _PRIVATE_NETWORKS):
            return False
    except (ValueError, AttributeError):
        return False
    auth = ws.headers.get("authorization", "")
    return auth.startswith("Bearer ") and hmac.compare_digest(auth[7:], _GUI_AUTH_TOKEN)


async def run_task(ws: WebSocket, message: dict) -> None:
    request = None
    active = True
    task_memory = None
    runner = asyncio.current_task()
    async def emit(frame):
        nonlocal task_memory
        if not active or (runner.cancelling() and frame.get("type") != "usage"):
            return
        if ws.client_state != WebSocketState.CONNECTED:
            return
        if frame.get("type") == "progress":
            task_memory = frame.get("task_memory")
        elif frame.get("type") in ("done", "error", "call_user") and task_memory is not None:
            frame = {**frame, "task_memory": task_memory}
        # Never attach incoming message/model_config to outgoing frames.
        if request:
            frame = {**frame, "session_id": request.session_id, "run_id": request.run_id}
        try:
            await ws.send_json(frame)
        except (RuntimeError, WebSocketDisconnect):
            pass

    try:
        request = RunRequest.parse(message)
        planner_mode = os.getenv("GUI_PLANNER", "vlm").strip().lower()
        request.model_config.validate(planner_mode)
    except ValueError as error:
        await emit({"type": "error", "phase": "validation", "error": str(error)})
        return

    usage = UsageLedger(request.session_id, emit)
    planner = Planner(request.model_config, mode=planner_mode, usage=usage)
    phase = "queue"
    try:
        await emit({"type": "queued"})
        who = {"owner_id": request.owner_id, "conversation_id": request.conversation_id, "run_id": request.run_id}
        async with _runtime.lease(who, request.session_id, request.queue_timeout, request.execution_timeout, "gui") as lease:
            phase = "execution"
            await emit({"type": "started"})
            operator, resumed = lease.operator, lease.resumed
            await emit({"type": "session", "resumed": resumed})
            graph = build(operator, emit, planner=planner)
            final = await graph.ainvoke({
                "instruction": request.instruction, "session_id": request.session_id,
                "step": 0, "max_steps": request.max_steps, "history": [], "status": "running",
            }, config={"recursion_limit": request.max_steps * 3 + 6})
            status = final.get("status", "ERROR")
            if status == "CALL_USER":
                await emit({"type": "call_user", "reason": final.get("call_user", ""), "usage": usage.summary()})
            elif status == "ERROR":
                await emit({"type": "error", "phase": phase, "error": final.get("error", "execution failed"), "usage": usage.summary()})
            else:
                await emit({"type": "done", "summary": final.get("result", ""),
                            "outcome": final.get("outcome", "partial"), "usage": usage.summary()})
    except QueueFull:
        await emit({"type": "error", "phase": "queue", "error": "GUI queue full; retry later", "usage": usage.summary()})
    except TimeoutError as error:
        phase = getattr(error, "phase", phase)
        await emit({"type": "done", "outcome": "partial", "phase": phase,
                    "summary": f"GUI {phase} timeout; inspect evidence before retrying", "usage": usage.summary()})
    except asyncio.CancelledError:
        # Keep the isolated context and completed actions for same-session resume.
        raise
    except Exception as error:
        # Do not echo provider errors, model config, or a request containing keys.
        await emit({"type": "error", "phase": phase, "error": f"GUI execution failed ({type(error).__name__})", "usage": usage.summary()})
    finally:
        # Old graph callbacks must not write into a reused WS connection.
        active = False


@app.websocket("/api/v1/exec/gui/ws")
async def gui_ws(ws: WebSocket) -> None:
    if not _authorized(ws):
        await ws.close(code=1008)
        return
    await ws.accept()
    try:
        while True:
            message = await ws.receive_json()
            if not isinstance(message, dict) or message.get("type") != "run":
                await ws.send_json({"type": "error", "error": "expected run request"})
                continue
            runner = asyncio.create_task(run_task(ws, message))
            try:
                while not runner.done():
                    try:
                        event = await asyncio.wait_for(ws.receive(), timeout=0.1)
                    except asyncio.TimeoutError:
                        continue
                    except (WebSocketDisconnect, RuntimeError):
                        runner.cancel()
                        break
                    if event.get("type") == "websocket.disconnect":
                        runner.cancel()
                        break
                await runner
            except asyncio.CancelledError:
                if ws.client_state == WebSocketState.CONNECTED:
                    raise
            finally:
                if not runner.done():
                    runner.cancel()
                    try:
                        await runner
                    except asyncio.CancelledError:
                        pass
            if ws.client_state != WebSocketState.CONNECTED:
                return
    except WebSocketDisconnect:
        return


@app.websocket("/api/v1/browser/cdp/ws")
async def browser_cdp_ws(ws: WebSocket) -> None:
    if not _authorized(ws):
        await ws.close(code=1008)
        return
    await ws.accept()
    await serve_bridge(ws, _runtime)
