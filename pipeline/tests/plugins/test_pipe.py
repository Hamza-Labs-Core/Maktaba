"""The pipeline's Windows connection to the plugin host (#710): the relay's handshake.

The real pipe is tested on Windows by cmd/maktaba-server/internal/pluginrelay's tests (the
relay, and this module loaded alone through it). Here the loop's ``create_pipe_connection``
is played by a socket pair, so the handshake runs on every OS: READY gives the streams;
a relay that closes before READY, or sends something else, raises ConnectionRefusedError
(the error the dispatcher already takes as "the host is not answering"); no pipe name
raises FileNotFoundError.
"""

from __future__ import annotations

import asyncio
import socket
from typing import Any

import pytest

from maktaba_pipeline.plugins import pipe

pytestmark = pytest.mark.asyncio


def _fake_pipe(first: bytes | None) -> Any:
    """A create_pipe_connection whose relay end sends ``first`` (None: closes at once),
    then echoes."""

    async def create_pipe_connection(factory: Any, name: str) -> Any:
        loop = asyncio.get_running_loop()
        ours, relay = socket.socketpair()
        relay.setblocking(False)

        async def relay_side() -> None:
            if first is None:
                relay.close()
                return
            await loop.sock_sendall(relay, first)
            data = await loop.sock_recv(relay, 64)
            await loop.sock_sendall(relay, data)
            relay.close()

        asyncio.ensure_future(relay_side())
        return await loop.create_connection(factory, sock=ours)

    return create_pipe_connection


async def test_ready_gives_the_streams(monkeypatch: pytest.MonkeyPatch) -> None:
    loop = asyncio.get_running_loop()
    monkeypatch.setattr(loop, "create_pipe_connection", _fake_pipe(pipe.READY), raising=False)
    r, w = await pipe.open_pipe(r"\\.\pipe\x", timeout=5)
    w.write(b"ping")
    await w.drain()
    assert await asyncio.wait_for(r.readexactly(4), 5) == b"ping"
    w.close()


@pytest.mark.parametrize("first", [None, b"\x00"], ids=["closed", "not-ready"])
async def test_no_ready_is_a_refused_connection(
    monkeypatch: pytest.MonkeyPatch, first: bytes | None
) -> None:
    loop = asyncio.get_running_loop()
    monkeypatch.setattr(loop, "create_pipe_connection", _fake_pipe(first), raising=False)
    with pytest.raises(ConnectionRefusedError):
        await pipe.open_pipe(r"\\.\pipe\x", timeout=5)


async def test_no_pipe_name_is_file_not_found(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv(pipe.ENV, raising=False)
    with pytest.raises(FileNotFoundError):
        pipe.pipe_name()
    monkeypatch.setenv(pipe.ENV, r"\\.\pipe\maktaba-plugind-abc")
    assert pipe.pipe_name() == r"\\.\pipe\maktaba-plugind-abc"
