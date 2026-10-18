"""Authenticated transport's page-only CDP protocol; no browser or model globals.

The websocket owns one operation. A separate reader can cancel acquisition or an
in-flight CDP command. Cleanup completes before its lease acknowledgement is sent.
"""
from __future__ import annotations

import asyncio
import json
import math
from urllib.parse import urlsplit

from starlette.websockets import WebSocketDisconnect
from service.runtime import QueueFull
from service.preview_document import decode_document, open_document
from service.browser_commands import READ_ONLY, context_lost
from service.browser_observation import execute_fixed, OBSERVATIONS, ACTIONS, REQUEST_KEYS

MAX_MESSAGE = 128 * 1024
MAX_RESULT = 128 * 1024
MAX_SCREENSHOT = 16 * 1024 * 1024
MAX_COMMANDS = 256
MAX_PREVIEW_MESSAGE = 1408 * 1024

# An explicit parameter boundary also prevents new upstream CDP options from
# silently granting browser-level privileges when dependencies are upgraded.
PARAMETERS = {
    "Orka.observe": {"operation", "request"},
    "Orka.act": {"operation", "request"},
    "Orka.getPageState": set(),
    "Orka.previewHTML": {"html_base64"},
    "Page.enable": {"enableFileChooserOpenedEvent"}, "Page.getFrameTree": set(),
    "Page.createIsolatedWorld": {"frameId", "worldName", "grantUniveralAccess"},
    "Page.navigate": {"url", "frameId", "referrer", "transitionType", "referrerPolicy"},
    "Page.captureScreenshot": {"format", "quality", "clip", "fromSurface", "captureBeyondViewport", "optimizeForSpeed"},
    "Runtime.enable": set(),
    "Runtime.evaluate": {"expression", "objectGroup", "contextId", "returnByValue", "awaitPromise", "silent", "userGesture", "generatePreview", "timeout", "throwOnSideEffect", "disableBreaks", "allowUnsafeEvalBlockedByCSP", "includeCommandLineAPI", "replMode"},
    "Runtime.callFunctionOn": {"functionDeclaration", "objectId", "executionContextId", "arguments", "objectGroup", "returnByValue", "awaitPromise", "silent", "userGesture", "generatePreview", "throwOnSideEffect"},
    "Runtime.getProperties": {"objectId", "ownProperties", "accessorPropertiesOnly", "generatePreview", "nonIndexedPropertiesOnly"},
    "Runtime.releaseObject": {"objectId"}, "Runtime.releaseObjectGroup": {"objectGroup"},
    "DOM.getDocument": {"depth", "pierce"},
    "DOM.querySelector": {"nodeId", "selector"},
    "DOM.describeNode": {"nodeId", "backendNodeId", "objectId", "depth", "pierce"},
    "DOM.resolveNode": {"nodeId", "backendNodeId", "objectGroup", "executionContextId"},
    "DOM.scrollIntoViewIfNeeded": {"nodeId", "backendNodeId", "objectId", "rect"},
    "DOM.getBoxModel": {"nodeId", "backendNodeId", "objectId"},
    "Input.dispatchMouseEvent": {"type", "x", "y", "modifiers", "timestamp", "button", "buttons", "clickCount", "force", "tangentialPressure", "tiltX", "tiltY", "twist", "deltaX", "deltaY", "pointerType"},
    "Input.dispatchKeyEvent": {"type", "modifiers", "timestamp", "text", "unmodifiedText", "keyIdentifier", "code", "key", "windowsVirtualKeyCode", "nativeVirtualKeyCode", "autoRepeat", "isKeypad", "isSystemKey", "location"},
    "Input.insertText": {"text"},
}


class BridgeError(Exception):
    def __init__(self, code, message):
        self.code, self.message = code, message
        super().__init__(message)


def fail(code="protocol_error", message="invalid browser request"):
    raise BridgeError(code, message)


def identity(value):
    keys = {"owner_id", "conversation_id", "run_id"}
    if not isinstance(value, dict) or set(value) != keys:
        fail("scope_mismatch", "trusted owner, conversation and run required")
    if any(not isinstance(v, str) or not v.strip() or len(v) > 256 for v in value.values()):
        fail("scope_mismatch", "invalid trusted identity")
    return dict(value)


def acquire_request(message):
    if message.get("type") != "acquire":
        fail(message="expected acquire")
    who = identity(message.get("identity"))
    operation = message.get("operation_id")
    if not isinstance(operation, str) or not operation.strip() or len(operation) > 256:
        fail(message="operation_id required")
    durations = []
    for key, default, maximum in (("queue_timeout", 30, 60), ("execution_timeout", 45, 60)):
        value = message.get(key, default)
        if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or not 0 < value <= maximum:
            fail(message="timeout must be positive and at most 60 seconds")
        durations.append(value)
    return who, operation, *durations


async def receive(ws):
    text = await ws.receive_text()
    size = len(text.encode("utf-8"))
    if size > MAX_PREVIEW_MESSAGE:
        fail("input_limit", "browser request exceeds 128 KiB")
    try:
        value = json.loads(text, parse_constant=lambda _: fail())
    except (ValueError, RecursionError):
        if size > MAX_MESSAGE:
            fail("input_limit", "browser request exceeds 128 KiB")
        fail(message="expected a JSON object")
    if not isinstance(value, dict):
        fail(message="expected a JSON object")
    if size > MAX_MESSAGE and not (value.get("type") == "command" and value.get("method") == "Orka.previewHTML"):
        fail("input_limit", "browser request exceeds 128 KiB")
    return value


def error_frame(error, scope, request=None):
    frame = {"type": "error", **scope, "error": {"code": error.code, "message": error.message}}
    if request is not None and request.get("type") == "command":
        frame.update(type="reply", id=request.get("id"))
    return frame


class PageChannel:
    def __init__(self, lease):
        self.lease = lease
        self.count = 0
        self.last_id = 0

    async def _validate(self, method, params, cdp):
        if method not in PARAMETERS or not isinstance(params, dict) or set(params) - PARAMETERS[method]:
            fail("unsupported_method", "CDP method or parameters are outside the page contract")
        state = self.lease.state
        if method in ("Orka.observe", "Orka.act"):
            allowed = OBSERVATIONS if method == "Orka.observe" else ACTIONS
            if not isinstance(params.get("operation"), str) or params["operation"] not in allowed:
                fail(message="invalid fixed page operation")
            request = params.get("request", {})
            if not isinstance(request, dict) or set(request) - REQUEST_KEYS:
                fail(message="invalid fixed page request")
        for key in ("enableFileChooserOpenedEvent", "replMode"):
            if key in params and params[key] is not False:
                fail(message="optional browser privilege flags must remain false")
        for key, allowed in (("contextId", state.worlds), ("executionContextId", state.worlds),
                             ("objectId", state.objects), ("nodeId", state.nodes), ("backendNodeId", state.backend_nodes)):
            if key in params and (not isinstance(params[key], (str, int)) or isinstance(params[key], bool) or params[key] not in allowed):
                fail("stale_ref", "CDP reference is outside the current page epoch")
        if method in ("Page.createIsolatedWorld", "Page.navigate"):
            tree = await cdp.send("Page.getFrameTree")
            main = tree["frameTree"]["frame"]["id"]
            if params.get("frameId", main) != main:
                fail("unsupported_frame", "only the main page frame is supported")
        if method == "Page.createIsolatedWorld":
            if params.get("worldName") != "orka-browser" or params.get("grantUniveralAccess", False) is not False:
                fail(message="only the page isolated world without universal access is allowed")
        if method == "Page.navigate":
            try:
                url = urlsplit(params.get("url", ""))
                valid = url.scheme in ("http", "https") and bool(url.hostname)
            except (ValueError, TypeError):
                valid = False
            if not valid:
                fail(message="navigation requires an HTTP(S) URL")
        if method in ("Runtime.evaluate", "Runtime.callFunctionOn"):
            field = "expression" if method == "Runtime.evaluate" else "functionDeclaration"
            script = params.get(field)
            if not isinstance(script, str):
                fail(message="page script must be a string")
            if len(script.encode("utf-8")) > 16 * 1024:
                fail("input_limit", "page script exceeds 16 KiB")
            if method == "Runtime.evaluate":
                if "contextId" not in params or params.get("includeCommandLineAPI", False) is not False or params.get("allowUnsafeEvalBlockedByCSP", False) is not False:
                    fail(message="evaluate requires the approved isolated world and page CSP")
                params["allowUnsafeEvalBlockedByCSP"] = False
            elif not ("objectId" in params or "executionContextId" in params):
                fail(message="callFunctionOn requires an approved world or object")
            arguments = params.get("arguments", [])
            if not isinstance(arguments, list):
                fail(message="function arguments must be an array")
            for arg in arguments:
                if not isinstance(arg, dict) or ("objectId" in arg and (not isinstance(arg["objectId"], str) or arg["objectId"] not in state.objects)):
                    fail("stale_ref", "argument object is outside the current page epoch")
        if method == "DOM.resolveNode" and "executionContextId" not in params:
            fail(message="resolveNode requires the approved isolated world")
        if method.startswith("DOM.") and method != "DOM.getDocument":
            if not any(key in params for key in ("nodeId", "backendNodeId", "objectId")):
                fail(message="a known page node or object is required")
        if method in ("DOM.getDocument", "DOM.describeNode"):
            depth = params.get("depth", 1)
            if isinstance(depth, bool) or not isinstance(depth, int) or not 0 <= depth <= 8:
                fail("input_limit", "DOM depth must be between 0 and 8")
        group = params.get("objectGroup")
        if group is not None:
            if not isinstance(group, str) or not group or len(group) > 128:
                fail(message="invalid object group")
            if method == "Runtime.releaseObjectGroup" and group not in state.groups:
                fail("stale_ref", "unknown page object group")

    def _remember(self, result):
        state = self.lease.state
        def walk(value):
            if isinstance(value, dict):
                # Do not adopt execution handles inside a by-value JS result or
                # iframe document. Only CDP's own structural fields grant handles.
                for name, values in (("objectId", state.objects), ("nodeId", state.nodes), ("backendNodeId", state.backend_nodes)):
                    if name in value and isinstance(value[name], (str, int)) and value[name]:
                        values.add(value[name])
                for key, child in value.items():
                    if key not in ("value", "contentDocument"):
                        walk(child)
            elif isinstance(value, list):
                for child in value:
                    walk(child)
        walk(result)

    async def execute(self, request):
        if not self.lease.active:
            fail("lease_expired", "browser lease has ended")
        if self.lease.uncertain:
            fail("outcome_unknown", "an earlier command is unconfirmed; lease must end")
        seq = request.get("id")
        if isinstance(seq, bool) or not isinstance(seq, int) or seq <= self.last_id:
            fail(message="command id must be a strictly increasing positive integer")
        self.last_id = seq
        self.count += 1
        if self.count > MAX_COMMANDS:
            fail("command_limit", "browser lease exceeds 256 commands")
        method, params = request.get("method"), request.get("params", {})
        cdp = await self.lease.cdp()
        if not isinstance(method, str):
            fail(message="CDP method required")
        params = dict(params) if isinstance(params, dict) else params
        await self._validate(method, params, cdp)
        document = None
        if method == "Orka.previewHTML":
            try:
                document = decode_document(params.get("html_base64"))
            except ValueError as error:
                fail("invalid_document", str(error))
        if method in ("Runtime.evaluate", "Runtime.callFunctionOn", "DOM.resolveNode"):
            params.setdefault("objectGroup", "orka-" + self.lease.lease_id)
        self.lease.uncertain = method not in READ_ONLY
        try:
            if method == "Orka.previewHTML":
                result = await open_document(self.lease.operator.page, document)
            elif method in ("Orka.observe", "Orka.act"):
                result = await execute_fixed(self.lease, cdp, params["operation"], params.get("request", {}))
            elif method == "Orka.getPageState":
                # Read browser events without a renderer round trip: even
                # getFrameTree can stall behind an uncommitted navigation.
                # The engine brackets observations and allows an event grace
                # period; this cache is evidence, not a future-event guarantee.
                result = {"loading": self.lease.state.loading, "revision": self.lease.state.revision}
            elif method == "Page.navigate":
                result = await self.lease.navigate(cdp, params)
            else:
                result = await cdp.send(method, params)
        except asyncio.CancelledError:
            raise
        except Exception as error:
            if method in ("Runtime.evaluate", "Runtime.callFunctionOn", "Orka.observe", "Orka.act") and context_lost(error):
                self.lease.uncertain = False
                if method in ("Orka.observe", "Orka.act"):
                    self.lease.state.observation_world = None
                fail("context_lost", "page execution context changed; observe the current document")
            # Protocol errors are explicit, but browser/provider text may contain
            # a sensitive expression/URL. Never echo that text in bridge errors.
            code = "outcome_unknown" if self.lease.uncertain else "cdp_error"
            fail(code, "page command failed; inspect page state before retrying")
        else:
            self.lease.uncertain = False
        if method == "Page.createIsolatedWorld":
            self.lease.state.worlds.add(result["executionContextId"])
        self._remember(result)
        if params.get("objectGroup") and method != "Runtime.releaseObjectGroup":
            self.lease.state.groups.add(params["objectGroup"])
        if method == "Runtime.releaseObject":
            self.lease.state.objects.discard(params["objectId"])
        if method == "Runtime.releaseObjectGroup":
            self.lease.state.groups.discard(params["objectGroup"])
            self.lease.state.objects.clear()
        limit = (MAX_SCREENSHOT * 4 // 3 + 4096) if method == "Page.captureScreenshot" else MAX_RESULT
        if len(json.dumps(result, ensure_ascii=False).encode("utf-8")) > limit:
            fail("output_limit", "page result exceeds the operation output limit")
        return result


async def _stop(task):
    if not task.done():
        task.cancel()
    try:
        await task
    except (asyncio.CancelledError, WebSocketDisconnect, BridgeError):
        pass


async def serve_bridge(ws, runtime):
    scope, lease, worker, reader = {}, None, None, None
    inbox = asyncio.Queue(maxsize=1)
    busy = False
    ended = False

    def current_scope():
        return lease.scope() if lease is not None else scope

    async def operation(who, op, queue_timeout, execution_timeout):
        nonlocal lease, busy
        try:
            async with runtime.lease(who, op, queue_timeout, execution_timeout, "browser") as acquired:
                lease = acquired
                channel = PageChannel(lease)
                await ws.send_json({"type":"acquired", **current_scope()})
                while True:
                    request = await inbox.get()
                    try:
                        result = await channel.execute(request)
                        await ws.send_json({"type":"reply", **current_scope(), "id":request["id"], "result":result})
                    except BridgeError as error:
                        await ws.send_json(error_frame(error, current_scope(), request))
                        if lease.uncertain:
                            return
                    finally:
                        busy = False
        except TimeoutError as error:
            phase = getattr(error, "phase", "queue")
            await ws.send_json({**error_frame(BridgeError("timeout", "browser " + phase + " timeout; inspect state before retrying"), current_scope()), "phase": phase})
        except QueueFull:
            await ws.send_json(error_frame(BridgeError("queue_full", "browser queue full; retry later"), current_scope()))
        except asyncio.CancelledError:
            raise
        except Exception:
            await ws.send_json(error_frame(BridgeError("outcome_unknown", "browser operation failed; inspect state before retrying"), current_scope()))

    try:
        request = await receive(ws)
        who, op, queue_timeout, execution_timeout = acquire_request(request)
        scope = {"identity":who, "operation_id":op}
        await ws.send_json({"type":"queued", **scope})
        worker = asyncio.create_task(operation(who, op, queue_timeout, execution_timeout))
        while True:
            reader = asyncio.create_task(receive(ws))
            if not ended:
                done, _ = await asyncio.wait((reader, worker), return_when=asyncio.FIRST_COMPLETED)
                if worker in done:
                    await worker
                    ended = True
            request = await reader
            reader = None
            expected = current_scope()
            if request.get("identity") != who or request.get("operation_id") != op or request.get("lease_id") != expected.get("lease_id"):
                await ws.send_json(error_frame(BridgeError("scope_mismatch", "request does not match this operation lease"), expected, request))
                continue
            kind = request.get("type")
            if kind in ("release", "cancel"):
                if lease is not None:
                    lease.release_requested = kind == "release" and not busy
                await _stop(worker)
                ended = True
                await ws.send_json({"type":"released" if kind == "release" else "cancelled", **current_scope()})
            elif kind == "observation_ack" and ended and lease is not None and lease.release_requested:
                # This extra client receipt follows the released frame. A lost
                # release/receipt leaves the next observation conservatively full.
                if lease.state.observation_delivery == lease.lease_id:
                    lease.state.observation_delivery = ""
            elif ended:
                await ws.send_json(error_frame(BridgeError("lease_expired", "browser lease has ended"), expected, request))
            elif kind != "command" or lease is None:
                await ws.send_json(error_frame(BridgeError("protocol_error", "expected a command on an acquired lease"), expected, request))
            elif busy:
                await ws.send_json(error_frame(BridgeError("protocol_error", "only one command may be in flight"), expected, request))
            else:
                busy = True
                inbox.put_nowait(request)
    except BridgeError as error:
        if worker is not None:
            await _stop(worker)
        await ws.send_json(error_frame(error, current_scope()))
    except WebSocketDisconnect:
        pass
    finally:
        if reader is not None:
            await _stop(reader)
        if worker is not None:
            await _stop(worker)
