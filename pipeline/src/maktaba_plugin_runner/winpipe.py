"""A runner's connections on Windows (Epic 54, story 54.23).

Inside its group's AppContainer a runner may not create a named pipe, and Python on Windows
has no AF_UNIX, so it cannot listen. The supervisor serves its pipes instead
(cmd/maktaba-server/internal/supervisor/runners_windows.go), and the runner only dials:

- **its back pipe** (``MAKTABA_RUNNER_BACK_PIPE``): it keeps connections waiting there; the
  supervisor pairs each caller (the worker, through the group's own pipe, or the health
  check) with one, sends it ``READY``, and from then on relays the bytes unchanged: the
  runner serves that connection as it serves one accepted on its socket elsewhere
  (:func:`serve_back`), and dials another to wait in its place;
- **its API pipe** (``MAKTABA_RUNNER_API_PIPE``): its plugin's statements reach the plugin
  API there, relayed to the API's socket (:class:`PipeTransport`), as the plugin host's
  relay does (#710). The relay sends ``READY`` once it reached the API.

Both pipes admit the supervisor's user and this group's AppContainer and no one else.
Standard library and the SDK only (CI's Windows runner step loads this outside a venv).
"""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import Awaitable, Callable
from typing import Any

from maktaba_plugin_sdk.native import UnixTransport

__all__ = [
    "API_ENV",
    "BACK_ENV",
    "PIPES_ENV",
    "READY",
    "PipeTransport",
    "connect",
    "open_stream",
    "serve_back",
]

READY = b"\x01"
BACK_ENV = "MAKTABA_RUNNER_BACK_PIPE"
API_ENV = "MAKTABA_RUNNER_API_PIPE"
#: the worker's: the prefix of the runners' pipes, which a group's name completes
PIPES_ENV = "MAKTABA_RUNNER_PIPES"

Streams = tuple[asyncio.StreamReader, asyncio.StreamWriter]


async def open_stream(name: str, *, limit: int = 1 << 20) -> Streams:
    """A stream pair on a pipe (the Proactor loop's; FileNotFoundError when none serves it)."""
    loop = asyncio.get_running_loop()
    reader = asyncio.StreamReader(limit=limit, loop=loop)
    protocol = asyncio.StreamReaderProtocol(reader, loop=loop)
    transport, _ = await loop.create_pipe_connection(lambda: protocol, name)  # type: ignore[attr-defined]
    return reader, asyncio.StreamWriter(transport, protocol, reader, loop)


async def connect(name: str, *, limit: int = 1 << 20, timeout: float = 10.0) -> Streams:
    """A stream pair on a relay's pipe once it says it reached the other side; otherwise
    ``ConnectionRefusedError``, as a refused unix socket."""
    r, w = await open_stream(name, limit=limit)
    try:
        first = await asyncio.wait_for(r.readexactly(1), timeout)
    except (asyncio.IncompleteReadError, TimeoutError, ConnectionError):
        w.close()
        raise ConnectionRefusedError(f"nothing answers through {name}") from None
    if first != READY:
        w.close()
        raise ConnectionRefusedError(f"{name} is not a Maktaba relay")
    return r, w


class PipeTransport(UnixTransport):
    """The plugin API through the runner's API pipe (the same HTTP/1.1 client)."""

    def __init__(self, pipe: str, token: str, *, pool: int = 8) -> None:
        super().__init__(pipe, token, pool=pool)
        self.pipe = pipe

    async def _conn(self) -> Streams:
        while self._idle:
            r, w = self._idle.pop()
            if not w.is_closing():
                return r, w
        return await connect(self.pipe)


async def serve_back(
    handle: Callable[[Any, Any], Awaitable[None]],
    name: str,
    *,
    waiting: int = 2,
    dial: Callable[[str], Awaitable[Any]] | None = None,
    retry: float = 0.5,
) -> None:
    """Serve the runner's callers from its back pipe until cancelled: ``waiting``
    connections wait at all times; one handed a caller (``READY``) is served by ``handle``
    and another is dialed in its place. A dial that fails (the supervisor not serving yet)
    is tried again after ``retry`` seconds; a waiting connection the supervisor closed is
    dialed again."""
    opener = dial or open_stream
    serving: set[asyncio.Task[None]] = set()

    async def wait_one() -> None:
        while True:
            try:
                r, w = await opener(name)
            except OSError:
                await asyncio.sleep(retry)
                continue
            try:
                first = await r.readexactly(1)
            except (asyncio.IncompleteReadError, ConnectionError):
                w.close()
                continue
            if first != READY:
                w.close()
                continue
            task = asyncio.ensure_future(handle(r, w))
            serving.add(task)
            task.add_done_callback(serving.discard)

    try:
        await asyncio.gather(*(wait_one() for _ in range(max(1, waiting))))
    finally:
        for task in list(serving):
            task.cancel()
        for task in list(serving):
            with contextlib.suppress(BaseException):
                await task
