"""LangGraph state machine: screenshot -> predict -> execute, looping until END.

Nodes are closures over the browser operator, the planner and an async `emit`
callback that streams WS frames (screenshot/action) as the loop runs. Conditional
edges route on status: running -> screenshot; END/ERROR/CALL_USER -> finish.
"""

from __future__ import annotations

import base64
import hashlib
from typing import Any, Awaitable, Callable

from langgraph.graph import END, StateGraph

from agent.evidence import Evidence
from agent.model import Planner
from agent.state import GraphState
from agent.task_memory import TaskMemory
from operators import marks as marksmod
from operators.remote_browser import RemoteBrowserOperator

EmitFn = Callable[[dict[str, Any]], Awaitable[None]]


def build(operator: RemoteBrowserOperator, emit: EmitFn, planner=None):
    planner = planner or Planner()
    evidence = Evidence()
    task_memory = TaskMemory(evidence)
    observation_source = {}
    last_memory = task_memory.snapshot()

    async def screenshot_node(state: GraphState) -> GraphState:
        dom = await operator.dom_snapshot()
        shot = await operator.screenshot()
        out: GraphState = {"dom": dom}
        if planner.mode == "uitars":
            # UI-TARS grounds on raw pixels (no Set-of-Marks — annotations are
            # off its training distribution). Keep a rolling screenshot window
            # for the model's multi-turn context.
            window = planner.uitars.max_shots if planner.uitars else 5
            shots = (list(state.get("shots", []))[-(window - 1):] if window > 1 else []) + [shot]
            out["screenshot"] = shot
            out["shots"] = shots
            await emit({"type": "screenshot", "data": shot, "session_id": state.get("session_id", "")})
            observation_frame = {"type": "observe", "mode": "uitars", "tokens": 1, "session_id": state.get("session_id", "")}
        elif planner.mode == "vlm":
            # Set-of-Marks: number interactive elements so the VLM can target
            # "mark N" instead of pixels.
            ms = await marksmod.collect_marks(operator.page)
            annotated = marksmod.marks_to_b64_annotated(base64.b64decode(shot), ms)
            out["screenshot"] = annotated
            out["marks"] = ms  # type: ignore[typeddict-unknown-key]
            out["marks_text"] = marksmod.marks_text(ms)  # type: ignore[typeddict-unknown-key]
            await emit({"type": "screenshot", "data": annotated, "session_id": state.get("session_id", "")})
            observation_frame = {"type": "observe", "mode": "vision", "marks": len(ms), "tokens": 1, "session_id": state.get("session_id", "")}
        else:
            if planner.mode == "llm":
                # Text Set-of-Marks: number the interactive elements but send
                # only their TEXT list to a cheap chat model — zero image tokens.
                ms = await marksmod.collect_marks(operator.page)
                out["marks"] = ms  # type: ignore[typeddict-unknown-key]
                out["marks_text"] = marksmod.marks_text(ms)  # type: ignore[typeddict-unknown-key]
                await emit({"type": "screenshot", "data": shot, "session_id": state.get("session_id", "")})
                observation_frame = {"type": "observe", "mode": "llm", "marks": len(ms), "tokens": 0, "session_id": state.get("session_id", "")}
            else:
                await emit({"type": "screenshot", "data": shot, "session_id": state.get("session_id", "")})
                observation_frame = {"type": "observe", "mode": "dom", "tokens": 0, "session_id": state.get("session_id", "")}
        observation = evidence.observe(int(state.get("step", 0)),
            "[page] " + evidence.clean(dom, 600) + "\n[elements] " + evidence.clean(out.get("marks_text", ""), 500))
        observation_source.update(observed_step=int(state.get("step", 0)),
            observation_seq=observation["seq"],
            screenshot_sha256=hashlib.sha256(out.get("screenshot", shot).encode()).hexdigest())
        out["task_memory"] = task_memory.snapshot()
        out["evidence"] = list(evidence.events)
        await emit({**observation_frame, "evidence": observation})
        return out

    async def predict_node(state: GraphState) -> GraphState:
        action, used_vision = await planner.predict(dict(state), operator.page)
        out: GraphState = {"prediction": action}
        raw = action.pop("_raw", None)
        if raw is not None:
            # fold the model's reply into the multi-turn history (uitars)
            out["responses"] = list(state.get("responses", [])) + [raw]
        return out

    async def execute_node(state: GraphState) -> GraphState:
        nonlocal last_memory
        action = state.get("prediction", {}) or {}
        # set-of-marks: resolve a mark index to a concrete element handle
        if "mark" in action and state.get("marks"):
            action = marksmod.mark_action_to_dict(action, state["marks"])  # type: ignore[typeddict-item]
        kind = action.get("action")
        step = int(state.get("step", 0)) + 1
        history = list(state.get("history", []))
        out: GraphState = {"step": step}
        # Classify input before recording model notes so secret parameters cannot
        # enter new progress fields. Notes describe this pre-action view only.
        prepared = await evidence.prepare(operator, action)
        progress = action.pop("progress", None)
        if not prepared.get("input_redacted"):
            task_memory.record(progress, observation_source)
        current_memory = task_memory.snapshot()
        if current_memory != last_memory:
            await emit({"type": "progress", "task_memory": current_memory,
                        "session_id": state.get("session_id", "")})
            last_memory = current_memory
        out["task_memory"] = current_memory

        if kind == "done":
            out["status"] = "END"
            out["outcome"] = "done"
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
        elif kind == "error":
            out["status"] = "ERROR"
            out["error"] = action.get("message", "error")
        else:
            try:
                receipt = prepared
                result = await operator.execute(action)
                receipt = evidence.executed(step, receipt, result)
                out["evidence"] = list(evidence.events)
                history.append({"action": action, "result": result})
                out["history"] = history
                target = receipt.get("target", "")
                if not target and "x" in action:
                    target = f"({action['x']:.0f},{action['y']:.0f})"
                await emit({
                    "type": "action",
                    "action": kind,
                    "target": target,
                    "result": receipt["result"],
                    "evidence": receipt,
                    "session_id": state.get("session_id", ""),
                })
                out["status"] = "running"
            except Exception as e:  # noqa: BLE001
                out["status"] = "ERROR"
                out["error"] = f"browser action failed ({type(e).__name__})"

        # Stop conditions: out of step budget, OR stuck (the last 3 actions are
        # identical — a scroll/click loop on a page the planner can't make
        # progress on). On stop, return a grounded page snapshot so the upstream
        # model can answer from what we DID see instead of re-invoking the
        # browser over and over.
        def _sig(a: dict[str, Any]) -> tuple:
            return (a.get("action"), a.get("url"), a.get("selector"), a.get("mark"),
                    a.get("x"), a.get("y"), a.get("text"), a.get("direction"),
                    a.get("dy"), a.get("key"), a.get("x2"), a.get("y2"))

        recent = [h["action"] for h in history[-3:] if h.get("action")]
        stuck = len(recent) == 3 and len({_sig(a) for a in recent}) == 1

        if out.get("status") == "running" and (step >= int(state.get("max_steps", 8)) or stuck):
            out["status"] = "END"
            try:
                snap = await operator.dom_snapshot(limit=1200)
            except Exception:  # noqa: BLE001
                snap = ""
            out["outcome"] = "partial"
            observation = evidence.observe(step, snap)
            out["evidence"] = list(evidence.events)
            await emit({"type": "observe", "evidence": observation, "session_id": state.get("session_id", "")})
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
