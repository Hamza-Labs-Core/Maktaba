"""A small asyncio HTTP/1.1 client for the local LLM runtimes.

The runtimes (Ollama, LM Studio, vLLM, llama.cpp server) listen on this machine; the
agent talks to them with JSON requests and reads streamed answers line by line (Ollama
NDJSON, OpenAI server-sent events). Cancelling the awaiting task closes the connection,
which is how a request the cluster gave up on is stopped. One connection per request
(``Connection: close``); ``Content-Length``, chunked and read-to-close bodies.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import ssl
import urllib.parse
from collections.abc import AsyncIterator
from dataclasses import dataclass
from typing import Any

__all__ = ["HTTPError", "Response", "get_json", "open_request", "post_json"]

MAX_HEADER = 64 * 1024
MAX_LINE = 16 * 1024 * 1024


class HTTPError(Exception):
    def __init__(self, status: int, body: str, url: str = "") -> None:
        super().__init__(f"HTTP {status} from {url}: {body[:300]}")
        self.status = status
        self.body = body
        self.url = url


@dataclass
class Response:
    status: int
    headers: dict[str, str]
    reader: asyncio.StreamReader
    writer: asyncio.StreamWriter
    url: str

    async def _chunks(self) -> AsyncIterator[bytes]:
        te = self.headers.get("transfer-encoding", "").lower()
        if "chunked" in te:
            while True:
                size_line = await self.reader.readline()
                if not size_line:
                    return
                size = int(size_line.split(b";", 1)[0].strip() or b"0", 16)
                if size == 0:
                    with contextlib.suppress(Exception):
                        await self.reader.readline()
                    return
                data = await self.reader.readexactly(size)
                await self.reader.readexactly(2)
                yield data
        elif "content-length" in self.headers:
            left = int(self.headers["content-length"])
            while left > 0:
                data = await self.reader.read(min(left, 65536))
                if not data:
                    return
                left -= len(data)
                yield data
        else:
            while True:
                data = await self.reader.read(65536)
                if not data:
                    return
                yield data

    async def read(self) -> bytes:
        out = bytearray()
        async for chunk in self._chunks():
            out += chunk
        return bytes(out)

    async def json(self) -> Any:
        return json.loads(await self.read() or b"null")

    async def lines(self) -> AsyncIterator[bytes]:
        buf = b""
        async for chunk in self._chunks():
            buf += chunk
            if len(buf) > MAX_LINE:
                raise ValueError("line too long")
            while b"\n" in buf:
                line, buf = buf.split(b"\n", 1)
                yield line.rstrip(b"\r")
        if buf.strip():
            yield buf

    async def aclose(self) -> None:
        self.writer.close()
        with contextlib.suppress(Exception):
            await self.writer.wait_closed()


async def open_request(
    method: str,
    url: str,
    *,
    body: Any = None,
    headers: dict[str, str] | None = None,
    connect_timeout: float = 5.0,
) -> Response:
    """Send the request and read the status line and headers; the caller reads the body
    and must :meth:`Response.aclose` it."""
    u = urllib.parse.urlsplit(url)
    if u.scheme not in ("http", "https") or not u.hostname:
        raise ValueError(f"not an http(s) URL: {url}")
    port = u.port or (443 if u.scheme == "https" else 80)
    ctx = ssl.create_default_context() if u.scheme == "https" else None
    reader, writer = await asyncio.wait_for(
        asyncio.open_connection(u.hostname, port, ssl=ctx, limit=MAX_LINE), connect_timeout
    )
    try:
        payload = b"" if body is None else json.dumps(body).encode("utf-8")
        path = (u.path or "/") + (f"?{u.query}" if u.query else "")
        host = u.hostname if u.port is None else f"{u.hostname}:{u.port}"
        if ":" in (u.hostname or "") and u.port is None:
            host = f"[{u.hostname}]"
        lines = [
            f"{method} {path} HTTP/1.1",
            f"Host: {host}",
            "Connection: close",
            "Accept: application/json, text/event-stream, application/x-ndjson",
            "User-Agent: maktaba-pipeline",
        ]
        if body is not None:
            lines += ["Content-Type: application/json", f"Content-Length: {len(payload)}"]
        for k, v in (headers or {}).items():
            lines.append(f"{k}: {v}")
        writer.write(("\r\n".join(lines) + "\r\n\r\n").encode("latin-1") + payload)
        await writer.drain()
        status_line = await reader.readline()
        parts = status_line.decode("latin-1").split(" ", 2)
        if len(parts) < 2 or not parts[0].startswith("HTTP/"):
            raise ConnectionError(f"bad response from {url}: {status_line[:100]!r}")
        status = int(parts[1])
        hdrs: dict[str, str] = {}
        total = 0
        while True:
            line = await reader.readline()
            total += len(line)
            if total > MAX_HEADER:
                raise ConnectionError("response headers too large")
            if line in (b"\r\n", b"\n", b""):
                break
            k, _, v = line.decode("latin-1").partition(":")
            hdrs[k.strip().lower()] = v.strip()
        return Response(status=status, headers=hdrs, reader=reader, writer=writer, url=url)
    except BaseException:
        writer.close()
        raise


async def _call(method: str, url: str, body: Any, timeout: float) -> Any:
    async def go() -> Any:
        resp = await open_request(method, url, body=body, connect_timeout=timeout)
        try:
            raw = await resp.read()
            if resp.status >= 400:
                raise HTTPError(resp.status, raw.decode("utf-8", "replace"), url)
            return json.loads(raw or b"null")
        finally:
            await resp.aclose()

    return await asyncio.wait_for(go(), timeout)


async def get_json(url: str, timeout: float = 5.0) -> Any:
    return await _call("GET", url, None, timeout)


async def post_json(url: str, body: Any, timeout: float = 600.0) -> Any:
    return await _call("POST", url, body, timeout)
