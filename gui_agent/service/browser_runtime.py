"""Single ownership boundary for visible GUI and page-scoped CDP operations."""
from __future__ import annotations

import asyncio
import uuid
import weakref
from contextlib import asynccontextmanager
from dataclasses import dataclass, field

from service.runtime import ExecutionQueue


class ExecutionTimeout(TimeoutError):
    """An execution deadline, distinct from waiting for the visible queue."""
    phase = "execution"


async def finish_cleanup(task):
    """Repeated caller cancellation cannot let an old page escape its lease."""
    cancelled = False
    while not task.done():
        try:
            await asyncio.shield(task)
        except asyncio.CancelledError:
            cancelled = True
    result = task.result()
    if cancelled:
        raise asyncio.CancelledError()
    return result


@dataclass
class PageState:
    page_id: str = field(default_factory=lambda: uuid.uuid4().hex)
    epoch: int = 1
    run_id: str = ""
    cdp: object = None
    worlds: set = field(default_factory=set)
    objects: set = field(default_factory=set)
    nodes: set = field(default_factory=set)
    backend_nodes: set = field(default_factory=set)
    groups: set = field(default_factory=set)

    def invalidate(self):
        self.epoch += 1
        self.worlds.clear()
        self.objects.clear()
        self.nodes.clear()
        self.backend_nodes.clear()
        self.groups.clear()


@dataclass
class BrowserLease:
    identity: dict
    operation_id: str
    operator: object
    resumed: bool
    state: PageState
    lease_id: str = field(default_factory=lambda: uuid.uuid4().hex)
    active: bool = True
    uncertain: bool = False

    def scope(self):
        return {"identity": dict(self.identity), "operation_id": self.operation_id,
                "lease_id": self.lease_id, "page_id": self.state.page_id,
                "page_epoch": self.state.epoch}

    async def cdp(self):
        if self.state.cdp is None:
            self.state.cdp = await self.operator.page.context.new_cdp_session(self.operator.page)
        return self.state.cdp


class BrowserRuntime:
    def __init__(self, pool, queue=None):
        self.pool = pool
        self.queue = queue if queue is not None else ExecutionQueue()
        self._pages = weakref.WeakKeyDictionary()
        self._quarantined = False

    def _state(self, page):
        state = self._pages.get(page)
        if state is None:
            state = PageState()
            self._pages[page] = state
            page.on("framenavigated", lambda frame: state.invalidate() if frame == page.main_frame else None)
            page.on("close", lambda *_: state.invalidate())
        return state

    async def _cleanup(self, lease):
        lease.active = False
        if not lease.uncertain and lease.state.cdp is not None:
            try:
                async with asyncio.timeout(.25):
                    for group in lease.state.groups:
                        await lease.state.cdp.send("Runtime.releaseObjectGroup", {"objectGroup": group})
            except Exception:
                lease.uncertain = True
        lease.state.objects.clear()
        lease.state.nodes.clear()
        lease.state.backend_nodes.clear()
        lease.state.groups.clear()
        if lease.uncertain:
            # Cancelling Playwright's await does not undo a dispatched command.
            # Close the page before making its visible queue available again.
            if lease.state.cdp is not None:
                try:
                    async with asyncio.timeout(.25):
                        await lease.state.cdp.send("Runtime.terminateExecution")
                except Exception:
                    pass
            try:
                async with asyncio.timeout(1):
                    await lease.operator.page.close()
            except BaseException:
                # Fail closed if Chromium cannot confirm that the page is gone.
                self._quarantined = True
            lease.state.invalidate()

    async def _abandon_acquisition(self, acquiring, identity, operation_id):
        try:
            async with asyncio.timeout(1):
                operator, resumed = await asyncio.shield(acquiring)
        except BaseException:
            # An unconfirmed context/page creation may still complete inside
            # Chromium. Refuse all subsequent leases until runtime replacement.
            self._quarantined = True
            acquiring.cancel()
            try:
                await acquiring
            except BaseException:
                pass
            return
        lease = BrowserLease(dict(identity), operation_id, operator, resumed,
                             self._state(operator.page), uncertain=True)
        await self._cleanup(lease)

    @asynccontextmanager
    async def lease(self, identity, operation_id, queue_timeout=30, execution_timeout=45, kind="browser"):
        async with self.queue.slot(queue_timeout):
            if self._quarantined:
                raise RuntimeError("browser runtime quarantined; page cleanup unconfirmed")
            lease = None
            try:
                async with asyncio.timeout(execution_timeout):
                    acquiring = asyncio.create_task(self.pool.get(identity["owner_id"], identity["conversation_id"]))
                    try:
                        operator, resumed = await asyncio.shield(acquiring)
                    except BaseException:
                        await finish_cleanup(asyncio.create_task(self._abandon_acquisition(acquiring, identity, operation_id)))
                        raise
                    state = self._state(operator.page)
                    if kind == "gui" or (state.run_id and state.run_id != identity["run_id"]):
                        state.invalidate()
                    state.run_id = identity["run_id"]
                    lease = BrowserLease(dict(identity), operation_id, operator, resumed, state)
                    yield lease
            except TimeoutError as error:
                if lease is not None and kind == "gui":
                    lease.uncertain = True
                raise ExecutionTimeout("browser execution deadline exceeded") from error
            except BaseException:
                if lease is not None and kind == "gui":
                    lease.uncertain = True
                raise
            finally:
                if lease is not None:
                    await finish_cleanup(asyncio.create_task(self._cleanup(lease)))
