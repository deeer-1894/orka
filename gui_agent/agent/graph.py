"""LangGraph state machine: screenshot -> predict -> execute, looping until END.

Nodes are closures over the browser operator, the planner and an async `emit`
callback that streams WS frames (screenshot/action) as the loop runs. Conditional
edges route on status: running -> screenshot; END/ERROR/CALL_USER -> finish.
"""

from __future__ import annotations

from typing import Any, Awaitable, Callable

from langgraph.graph import END, StateGraph

from agent.model import Planner
from agent.state import GraphState
from operators.remote_browser import RemoteBrowserOperator

EmitFn = Callable[[dict[str, Any]], Awaitable[None]]


def build(operator: RemoteBrowserOperator, emit: EmitFn):
    planner = Planner()

    async def screenshot_node(state: GraphState) -> GraphState:
        dom = await operator.dom_snapshot()
        shot = await operator.screenshot()
        out: GraphState = {"dom": dom}
        # Always stream a screenshot for UI feedback (no token cost), but only
        # feed it to the VLM when DOM-first failed (use_vision) — keeping the
        # vision-token cost at zero on the DOM-first happy path.
        await emit({"type": "screenshot", "data": shot, "session_id": state.get("session_id", "")})
        if state.get("use_vision"):
            out["screenshot"] = shot
        mode = "vision" if state.get("use_vision") else "dom"
        await emit({"type": "observe", "mode": mode, "tokens": 0 if mode == "dom" else 1})
        return out

    async def predict_node(state: GraphState) -> GraphState:
        action, used_vision = await planner.predict(dict(state), operator.page)
        out: GraphState = {"prediction": action}
        if used_vision:
            out["use_vision"] = True
        return out

    async def execute_node(state: GraphState) -> GraphState:
        action = state.get("prediction", {}) or {}
        kind = action.get("action")
        step = int(state.get("step", 0)) + 1
        history = list(state.get("history", []))
        out: GraphState = {"step": step}

        if kind == "done":
            out["status"] = "END"
            out["result"] = action.get("result", await operator.title())
        elif kind == "call_user":
            out["status"] = "CALL_USER"
            out["call_user"] = action.get("reason", "user input required")
            await emit({"type": "call_user", "reason": out["call_user"], "session_id": state.get("session_id", "")})
        elif kind == "error":
            out["status"] = "ERROR"
            out["error"] = action.get("message", "error")
            await emit({"type": "error", "error": out["error"], "session_id": state.get("session_id", "")})
        else:
            try:
                result = await operator.execute(action)
                history.append({"action": action, "result": result})
                out["history"] = history
                await emit({
                    "type": "action",
                    "action": kind,
                    "target": action.get("url") or action.get("selector") or "",
                    "result": result,
                    "session_id": state.get("session_id", ""),
                })
                out["status"] = "running"
            except Exception as e:  # noqa: BLE001
                out["status"] = "ERROR"
                out["error"] = str(e)
                await emit({"type": "error", "error": str(e), "session_id": state.get("session_id", "")})

        if out.get("status") == "running" and step >= int(state.get("max_steps", 8)):
            out["status"] = "END"
            out["result"] = "max steps reached"
        return out

    def route(state: GraphState) -> str:
        return "continue" if state.get("status") == "running" else "finish"

    g = StateGraph(GraphState)
    g.add_node("screenshot", screenshot_node)
    g.add_node("predict", predict_node)
    g.add_node("execute", execute_node)
    g.set_entry_point("screenshot")
    g.add_edge("screenshot", "predict")
    g.add_edge("predict", "execute")
    g.add_conditional_edges("execute", route, {"continue": "screenshot", "finish": END})
    return g.compile()
