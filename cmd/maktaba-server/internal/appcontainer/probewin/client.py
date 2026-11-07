"""The pipeline's side of #710's probe: can asyncio on Windows reach the plugin host?

    python client.py facts
    python client.py uds <path>     AF_UNIX through asyncio (if the socket module has it)
    python client.py pipe <name>    a named pipe through the Proactor loop
"""

from __future__ import annotations

import asyncio
import socket
import sys


async def uds(path: str) -> None:
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)  # type: ignore[attr-defined]
    s.setblocking(False)
    loop = asyncio.get_running_loop()
    await loop.sock_connect(s, path)
    r, w = await asyncio.open_connection(sock=s)
    w.write(b"ping\n")
    await w.drain()
    line = await asyncio.wait_for(r.readline(), 10)
    assert line.strip() == b"pong", line
    w.close()


async def pipe(name: str) -> None:
    loop = asyncio.get_running_loop()
    got: asyncio.Future[bytes] = loop.create_future()

    class P(asyncio.Protocol):
        def connection_made(self, t):  # type: ignore[no-untyped-def]
            t.write(b"ping\n")

        def data_received(self, data: bytes) -> None:
            if not got.done():
                got.set_result(data)

    t, _ = await loop.create_pipe_connection(P, name)  # type: ignore[attr-defined]
    data = await asyncio.wait_for(got, 10)
    assert data.strip() == b"pong", data
    t.close()


def main() -> int:
    mode = sys.argv[1]
    if mode == "facts":
        print("FACT python", sys.version.replace("\n", " "))
        print("FACT socket.AF_UNIX", hasattr(socket, "AF_UNIX"))
        print("FACT loop", type(asyncio.new_event_loop()).__name__)
        return 0
    try:
        asyncio.run(uds(sys.argv[2]) if mode == "uds" else pipe(sys.argv[2]))
    except Exception as exc:  # noqa: BLE001 - the probe reports
        print("RESULT py-" + mode, "FAIL", type(exc).__name__, exc)
        return 1
    print("RESULT py-" + mode, "OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
