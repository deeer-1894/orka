"""Bounded FIFO access to the single visible execution window."""
import asyncio
from contextlib import asynccontextmanager


class QueueFull(Exception):
    pass


class ExecutionQueue:
    def __init__(self, max_waiters=8):
        self.max_waiters = max_waiters
        self.waiting = 0
        self._lock = asyncio.Lock()

    @asynccontextmanager
    async def slot(self, timeout):
        if self._lock.locked() and self.waiting >= self.max_waiters:
            raise QueueFull("GUI execution queue is full")
        self.waiting += 1
        try:
            async with asyncio.timeout(timeout):
                await self._lock.acquire()
        finally:
            self.waiting -= 1
        try:
            yield
        finally:
            self._lock.release()
