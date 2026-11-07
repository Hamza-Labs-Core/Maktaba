"""The plugin API over this machine's unix socket (Epic 54, story 54.04 §1, 54.24 §5).

A small HTTP/1.1 client on ``asyncio`` (no extra dependency in the runner): JSON in and
out, ``Authorization: Plugin <token>``, kept-alive connections reused per call. Every
machine's own Go API serves the socket, so a ``placement:any`` stage on a node reaches the
home's database through the node's API, with that machine's role.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
from typing import Any

from .stmtdb import PluginRefused

__all__ = ["UnixTransport"]

MAX_RESPONSE = 1 << 30  # the API's response cap (54.24 edge cases)


class UnixTransport:
    def __init__(self, socket_path: str, token: str, *, pool: int = 8) -> None:
        self.socket_path = socket_path
        self.token = token
        self._idle: list[tuple[asyncio.StreamReader, asyncio.StreamWriter]] = []
        self._pool = pool

    async def _conn(self) -> tuple[asyncio.StreamReader, asyncio.StreamWriter]:
        while self._idle:
            r, w = self._idle.pop()
            if not w.is_closing():
                return r, w
        return await asyncio.open_unix_connection(self.socket_path, limit=1 << 20)

    def _give_back(self, r: asyncio.StreamReader, w: asyncio.StreamWriter) -> None:
        if len(self._idle) < self._pool and not w.is_closing():
            self._idle.append((r, w))
        else:
            w.close()

    async def call(self, fn: str, body: dict[str, Any]) -> dict[str, Any]:
        data = json.dumps(body, separators=(",", ":"), ensure_ascii=False).encode()
        head = (
            f"POST /plugin-api/v1/{fn} HTTP/1.1\r\nHost: plugin-api\r\n"
            f"Authorization: Plugin {self.token}\r\nContent-Type: application/json\r\n"
            f"Content-Length: {len(data)}\r\n\r\n"
        ).encode()
        for attempt in (0, 1):
            r, w = await self._conn()
            try:
                w.write(head + data)
                await w.drain()
                status, headers = await _read_head(r)
            except (ConnectionError, asyncio.IncompleteReadError):
                w.close()
                if attempt:
                    raise
                continue  # a kept-alive connection the API closed meanwhile: once more
            try:
                n = int(headers.get("content-length", "0"))
                if n > MAX_RESPONSE:
                    raise PluginRefused(413, "plugin_too_large", "response over 1 GB")
                raw = await r.readexactly(n) if n else b"{}"
            except BaseException:
                w.close()
                raise
            if headers.get("connection", "").lower() == "close":
                w.close()
            else:
                self._give_back(r, w)
            out = json.loads(raw or b"{}")
            if status >= 300:
                raise PluginRefused(
                    status, str(out.get("code") or status), str(out.get("detail") or "")
                )
            return out if isinstance(out, dict) else {"value": out}
        raise ConnectionError("plugin API unreachable")  # pragma: no cover

    async def close(self) -> None:
        for _, w in self._idle:
            w.close()
            with contextlib.suppress(Exception):
                await w.wait_closed()
        self._idle.clear()


async def _read_head(r: asyncio.StreamReader) -> tuple[int, dict[str, str]]:
    line = await r.readuntil(b"\r\n")
    parts = line.decode("latin-1").split(" ", 2)
    status = int(parts[1])
    headers: dict[str, str] = {}
    while True:
        h = await r.readuntil(b"\r\n")
        if h == b"\r\n":
            return status, headers
        k, _, v = h.decode("latin-1").partition(":")
        headers[k.strip().lower()] = v.strip()
