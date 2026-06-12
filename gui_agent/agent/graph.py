"""LangGraph state machine: screenshot -> predict -> execute, looping until END.

Nodes are closures over the browser operator, the planner and an async `emit`
callback that streams WS frames (screenshot/action) as the loop runs. Conditional
edges route on status: running -> screenshot; END/ERROR/CALL_USER -> finish.
"""

from __future__ import annotations

import base64
from typing import Any, Awaitable, Callable

from langgraph.graph import END, StateGraph

from agent.model import Planner
from agent.state import GraphState
from operators import marks as marksmod
from operators.remote_browser import RemoteBrowserOperator

EmitFn = Callable[[dict[str, Any]], Awaitable[None]]


def build(operator: RemoteBrowserOperator, emit: EmitFn):
    planner = Planner()

    async def screenshot_node(state: GraphState) -> GraphState:
        dom = await operator.dom_snapshot()
        shot = await operator.screenshot()
        out: GraphState = {"dom": dom}
        if planner.mode == "uitars":
            # UI-TARS grounds on raw pixels (no Set-of-Marks — annotations are
            # off its training distribution). Keep a rolling screenshot window
            # for the model's multi-turn context.
            window = planner.uitars.max_shots if planner.uitars else 5
            shots = list(state.get("shots", []))[-(window - 1):] + [shot]
            out["screenshot"] = shot
            out["shots"] = shots
            await emit({"type": "screenshot", "data": shot, "session_id": state.get("session_id", "")})
            await emit({"type": "observe", "mode": "uitars", "tokens": 1, "session_id": state.get("session_id", "")})
        elif state.get("use_vision"):
            # Set-of-Marks: number interactive elements so the VLM can target
            # "mark N" instead of pixels.
            ms = await marksmod.collect_marks(operator.page)
            annotated = marksmod.marks_to_b64_annotated(base64.b64decode(shot), ms)
            out["screenshot"] = annotated
            out["marks"] = ms  # type: ignore[typeddict-unknown-key]
            out["marks_text"] = marksmod.marks_text(ms)  # type: ignore[typeddict-unknown-key]
            await emit({"type": "screenshot", "data": annotated, "session_id": state.get("session_id", "")})
            await emit({"type": "observe", "mode": "vision", "marks": len(ms), "tokens": 1, "session_id": state.get("session_id", "")})
        else:
            if planner.mode == "llm":
                # Text Set-of-Marks: number the interactive elements but send
                # only their TEXT list to a cheap chat model — zero image tokens.
                ms = await marksmod.collect_marks(operator.page)
                out["marks"] = ms  # type: ignore[typeddict-unknown-key]
                out["marks_text"] = marksmod.marks_text(ms)  # type: ignore[typeddict-unknown-key]
                await emit({"type": "screenshot", "data": shot, "session_id": state.get("session_id", "")})
                await emit({"type": "observe", "mode": "llm", "marks": len(ms), "tokens": 0, "session_id": state.get("session_id", "")})
            else:
                await emit({"type": "screenshot", "data": shot, "session_id": state.get("session_id", "")})
                await emit({"type": "observe", "mode": "dom", "tokens": 0, "session_id": state.get("session_id", "")})
        return out

    async def predict_node(state: GraphState) -> GraphState:
        action, used_vision = await planner.predict(dict(state), operator.page)
        out: GraphState = {"prediction": action}
        raw = action.pop("_raw", None)
        if raw is not None:
            # fold the model's reply into the multi-turn history (uitars)
            out["responses"] = list(state.get("responses", [])) + [raw]
        if used_vision:
            out["use_vision"] = True
        return out

    async def execute_node(state: GraphState) -> GraphState:
        action = state.get("prediction", {}) or {}
        # set-of-marks: resolve a mark index to a concrete element handle
        if "mark" in action and state.get("marks"):
            action = marksmod.mark_action_to_dict(action, state["marks"])  # type: ignore[typeddict-item]
        kind = action.get("action")
        step = int(state.get("step", 0)) + 1
        history = list(state.get("history", []))
        out: GraphState = {"step": step}

        if kind == "done":
            out["status"] = "END"
            summary = action.get("result", "") or await operator.title()
            # Ground the answer: attach the page's real url + readable text so the
            # upstream model corrects/confirms the planner's summary instead of
            # trusting a possibly-hallucinated one-liner (e.g. echoing the query).
            try:
                page_read = await operator.dom_snapshot(limit=1200)
            except Exception:
                page_read = ""
            url = ""
            try:
                url = operator.page.url
            except Exception:
                pass
            out["result"] = f"{summary}\n\n[final url] {url}\n[page content]\n{page_read}".strip()
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
                target = action.get("url") or action.get("selector") or ""
                if not target and "x" in action:
                    target = f"({action['x']:.0f},{action['y']:.0f})"
                await emit({
                    "type": "action",
                    "action": kind,
                    "target": target,
                    "thought": action.get("_thought", ""),
                    "result": result,
                    "session_id": state.get("session_id", ""),
                })
                out["status"] = "running"
            except Exception as e:  # noqa: BLE001
                out["status"] = "ERROR"
                out["error"] = str(e)
                await emit({"type": "error", "error": str(e), "session_id": state.get("session_id", "")})

        # Stop conditions: out of step budget, OR stuck (the last 3 actions are
        # identical — a scroll/click loop on a page the planner can't make
        # progress on). On stop, return a grounded page snapshot so the upstream
        # model can answer from what we DID see instead of re-invoking the
        # browser over and over.
        def _sig(a: dict[str, Any]) -> tuple:
            return (a.get("action"), a.get("url") or a.get("selector") or a.get("mark") or a.get("x"))

        recent = [h["action"] for h in history[-3:] if h.get("action")]
        stuck = len(recent) == 3 and len({_sig(a) for a in recent}) == 1

        if out.get("status") == "running" and (step >= int(state.get("max_steps", 8)) or stuck):
            out["status"] = "END"
            try:
                snap = await operator.dom_snapshot(limit=1200)
            except Exception:  # noqa: BLE001
                snap = ""
            reason = "重复动作,无进展" if stuck else "达到步数上限"
            out["result"] = f"[浏览器停止:{reason}。以下是当前页面内容,请据此作答,不要再重复打开浏览器]\n{snap}"
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
