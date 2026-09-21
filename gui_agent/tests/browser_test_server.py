"""Disposable localhost bridge for Go contracts; no production configuration.

Run in an isolated test container with /app pointing at gui_agent. The literal
token is fixture-only. This module never imports the production server.
"""
from contextlib import asynccontextmanager
import os

from starlette.applications import Starlette
from starlette.routing import WebSocketRoute, Route
from starlette.responses import JSONResponse
import uvicorn

from operators.sessions import SessionPool
from service.browser_runtime import BrowserRuntime
from service.browser_bridge import serve_bridge, identity, BridgeError


@asynccontextmanager
async def lifespan(app):
    pool = SessionPool(headless=os.getenv("BROWSER_TEST_HEADFUL") != "1")
    app.state.runtime = BrowserRuntime(pool)
    await pool.start()
    try:
        yield
    finally:
        await pool.close()


async def bridge(ws):
    if ws.headers.get("authorization") != "Bearer browser-recovery-fixture":
        await ws.close(code=1008)
        return
    await ws.accept()
    await serve_bridge(ws, ws.app.state.runtime)


async def gui_handoff(request):
    # Fixture-only cross-language coverage of the real GUI lease boundary.
    if request.headers.get("authorization") != "Bearer browser-recovery-fixture":
        return JSONResponse({"error": "unauthorized"}, status_code=401)
    try:
        who = identity(await request.json())
    except (BridgeError, ValueError):
        return JSONResponse({"error": "invalid_fixture_identity"}, status_code=400)
    async with request.app.state.runtime.lease(who, "fixture-gui-handoff", 3, 3, "gui") as lease:
        return JSONResponse({"page_id": lease.state.page_id, "page_epoch": lease.state.epoch})


app = Starlette(routes=[
    WebSocketRoute("/api/v1/browser/cdp/ws", bridge),
    Route("/fixture/gui-handoff", gui_handoff, methods=["POST"]),
], lifespan=lifespan)

if __name__ == "__main__":
    uvicorn.run(app, host="127.0.0.1", port=8765, log_level="warning")
