"""Fixed page helpers in a world inaccessible to arbitrary client JavaScript.

The bundle is generated from browsertool's canonical helpers. No caller can
supply executable source, a context ID, or a read-only privilege assertion.
"""
import json
import uuid
from pathlib import Path

SCRIPTS = json.loads(Path(__file__).with_name("browser_helpers.json").read_text())
OBSERVATIONS = frozenset({"snapshot", "settle", "wait", "commit_observation"})
ACTIONS = frozenset({"click", "press", "fill", "select", "scroll", "prepare"})
REQUEST_KEYS = frozenset({
    "action", "view", "frame", "ref", "snapshot_id", "selector", "text",
    "value", "condition", "url", "direction", "amount",
    "text_limit", "element_limit", "byte_limit",
})


def by_value(result):
    # Exception RemoteObjects would otherwise grant a handle into this private
    # world via PageChannel._remember. Only by-value data crosses the boundary.
    if result.get("exceptionDetails"):
        return {"exceptionDetails": {"text": "fixed page helper failed"}}
    return {"result": {"value": result.get("result", {}).get("value")}}


async def execute_fixed(lease, cdp, operation, request):
    state = lease.state
    if state.observation_world is None:
        epoch = state.epoch
        tree = await cdp.send("Page.getFrameTree")
        world = await cdp.send("Page.createIsolatedWorld", {
            "frameId": tree["frameTree"]["frame"]["id"], "worldName": "orka-observer",
        })
        # Never add this handle to state.worlds: generic CDP clients cannot use
        # it to redefine globals/prototypes used by the trusted read helpers.
        world_id = world["executionContextId"]
        installed = await cdp.send("Runtime.evaluate", {
            "contextId": world_id,
            "expression": "(" + SCRIPTS["dom"] + ")();(" + SCRIPTS["observation"] + ")()",
            "returnByValue": True, "allowUnsafeEvalBlockedByCSP": False,
        })
        if installed.get("exceptionDetails"):
            raise RuntimeError("fixed observation helpers could not be initialized")
        if state.epoch != epoch:
            raise RuntimeError("execution context was destroyed")
        state.observation_world = world_id
    if operation == "settle":
        return by_value(await cdp.send("Runtime.evaluate", {
            "contextId": state.observation_world, "expression": SCRIPTS["settle"],
            "returnByValue": True, "allowUnsafeEvalBlockedByCSP": False,
        }))
    payload = {**request, "operation": operation, "scope": [
        lease.identity["owner_id"], lease.identity["conversation_id"],
        lease.identity["run_id"], state.page_id, state.epoch], "nonce": uuid.uuid4().hex}
    declaration = SCRIPTS["snapshot"]
    arguments = [{"value": payload}]
    reset_baseline = operation == "snapshot" and state.observation_delivery not in ("", lease.lease_id)
    if reset_baseline:
        declaration = """function(q) {
          const slot = globalThis.__orkaBrowserState;
          if (slot) {slot.observation = null; slot.pendingObservation = null; slot.progress = null;}
          return (""" + SCRIPTS["snapshot"] + """)(q);
        }"""
    if operation == "commit_observation":
        # A lost commit reply or caller cancellation must not establish a
        # baseline the caller never received. Cleanup resolves this transaction.
        lease.observation_commit = (state.observation_world, state.epoch)
        state.observation_delivery = lease.lease_id
        declaration = """function(q, lease) {
          const slot = globalThis.__orkaBrowserState;
          if (slot && slot.leaseCommit?.lease !== lease) slot.leaseCommit = {
            lease, scope: slot.scope, observation: slot.observation, progress: slot.progress
          };
          return (""" + SCRIPTS["snapshot"] + """)(q);
        }"""
        arguments.append({"value": lease.lease_id})
    result = await cdp.send("Runtime.callFunctionOn", {
        "executionContextId": state.observation_world,
        "functionDeclaration": declaration, "arguments": arguments,
        "returnByValue": True,
    })
    if reset_baseline:
        state.observation_delivery = ""
    return by_value(result)


async def finish_observation(lease):
    world, epoch = lease.observation_commit
    if lease.state.epoch != epoch or lease.state.observation_world != world:
        return
    result = await lease.state.cdp.send("Runtime.callFunctionOn", {
        "executionContextId": world,
        "functionDeclaration": """function(lease, accept) {
          const slot = globalThis.__orkaBrowserState, tx = slot?.leaseCommit;
          if (!tx || tx.lease !== lease) return;
          if (!accept && tx.scope === slot.scope) {
            slot.observation = tx.observation; slot.progress = tx.progress;
          }
          slot.leaseCommit = null;
        }""",
        "arguments": [{"value": lease.lease_id}, {"value": lease.release_requested}],
        "returnByValue": True,
    })
    if result.get("exceptionDetails"):
        raise RuntimeError("observation finalization was not confirmed")
