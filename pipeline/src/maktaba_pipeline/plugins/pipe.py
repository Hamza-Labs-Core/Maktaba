"""The plugin host's dispatch connection on Windows (#710).

Python on Windows has no AF_UNIX, and the host (maktaba-plugind, in its AppContainer) may
not create a named pipe. So the supervisor serves a named pipe for the pipeline, named for
each of its runs in ``MAKTABA_PLUGIND_PIPE``, admitting only its own user, and relays each
connection byte for byte to the host's socket
(cmd/maktaba-server/internal/pluginrelay). It sends one byte, ``READY``, once it reached
the host, and closes the connection without it when it could not: ``open_pipe`` raises
``ConnectionRefusedError`` then, the error a refused unix socket raises, so the dispatcher
falls back the same way on every OS. After ``READY`` the host's frames pass unchanged.

Standard library only: CI's relay test loads this file alone.
"""

from __future__ import annotations

import asyncio
import os

ENV = "MAKTABA_PLUGIND_PIPE"
READY = b"\x01"

__all__ = ["ENV", "READY", "open_pipe", "pipe_name"]


def pipe_name() -> str:
    """The relay's pipe (``MAKTABA_PLUGIND_PIPE``); FileNotFoundError when it is not set."""
    name = os.environ.get(ENV, "").strip()
    if not name:
        raise FileNotFoundError(f"{ENV} is not set: no plugin host relay on this machine")
    return name


async def open_pipe(
    name: str, *, limit: int = 1 << 20, timeout: float = 10.0
) -> tuple[asyncio.StreamReader, asyncio.StreamWriter]:
    """A stream pair on the relay's pipe, once the relay says the host answered."""
    loop = asyncio.get_running_loop()
    reader = asyncio.StreamReader(limit=limit, loop=loop)
    protocol = asyncio.StreamReaderProtocol(reader, loop=loop)
    # the Proactor loop's (Windows); FileNotFoundError when no relay serves the name
    transport, _ = await loop.create_pipe_connection(lambda: protocol, name)  # type: ignore[attr-defined]
    writer = asyncio.StreamWriter(transport, protocol, reader, loop)
    try:
        first = await asyncio.wait_for(reader.readexactly(1), timeout)
    except (asyncio.IncompleteReadError, TimeoutError, ConnectionError):
        writer.close()
        raise ConnectionRefusedError(f"the plugin host is not answering through {name}") from None
    if first != READY:
        writer.close()
        raise ConnectionRefusedError(f"{name} is not the plugin host's relay")
    return reader, writer
