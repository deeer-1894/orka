"""FastAPI WebSocket service for the GUI executor.

Protocol (control layer <-> gui_agent):
  in:  {"type":"run","instruction":..,"session_id":..,"max_steps":..,"cdp_url":..}
  out: {"type":"screenshot","data":b64,...} | {"type":"observe","mode":..} |
       {"type":"action","action":..,"target":..,"result":..} |
       {"type":"call_user","reason":..} | {"type":"done","summary":..} |
       {"type":"error","error":..}
"""

from __future__ import annotations

import os
from typing import Any

from fastapi import FastAPI, WebSocket, WebSocketDisconnect

from agent.graph import build
from operators.remote_browser import RemoteBrowserOperator

app = FastAPI(title="cavis-gui-agent")


@app.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok"}


async def run_task(ws: WebSocket, msg: dict[str, Any]) -> None:
    session_id = msg.get("session_id", "")
    instruction = msg.get("instruction", "")
    max_steps = int(msg.get("max_steps", 8))
    cdp_url = msg.get("cdp_url") or os.getenv("CDP_URL", "")

    async def emit(frame: dict[str, Any]) -> None:
        await ws.send_json(frame)

    operator = RemoteBrowserOperator(cdp_url=cdp_url)
    try:
        await operator.start()
    except Exception as e:  # noqa: BLE001
        await emit({"type": "error", "error": f"browser start failed: {e}", "session_id": session_id})
        return

    try:
        graph = build(operator, emit)
        init = {
            "instruction": instruction,
            "session_id": session_id,
            "step": 0,
            "max_steps": max_steps,
            "history": [],
            "status": "running",
            "use_vision": False,
        }
        final = await graph.ainvoke(init, config={"recursion_limit": max_steps * 3 + 6})
        status = final.get("status", "END")
        if status == "CALL_USER":
            await emit({"type": "call_user", "reason": final.get("call_user", ""), "session_id": session_id})
        elif status == "ERROR":
            await emit({"type": "error", "error": final.get("error", "error"), "session_id": session_id})
        else:
            await emit({"type": "done", "summary": final.get("result", ""), "session_id": session_id})
    except Exception as e:  # noqa: BLE001
        await emit({"type": "error", "error": str(e), "session_id": session_id})
    finally:
        await operator.close()


@app.websocket("/api/v1/exec/gui/ws")
async def gui_ws(ws: WebSocket) -> None:
    await ws.accept()
    try:
        while True:
            msg = await ws.receive_json()
            if msg.get("type") == "run":
                await run_task(ws, msg)
            else:
                await ws.send_json({"type": "error", "error": f"unknown message type {msg.get('type')}"})
    except WebSocketDisconnect:
        return
