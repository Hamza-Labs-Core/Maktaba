"""Fakes for the agent's integration tests: a hub (websockets) and an LLM runtime
(http.server answering like Ollama and like an OpenAI-compatible server)."""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import threading
import time
from collections.abc import AsyncIterator, Callable, Coroutine, Iterator
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

from websockets.asyncio.server import Server, ServerConnection, serve

from maktaba_pipeline.cluster import crypto
from maktaba_pipeline.cluster.agent.codec import (
    Opener,
    Sealer,
    client_proof,
    derive_keys,
    dh,
    server_proof,
    transcript_hash,
)

# --- the hub -------------------------------------------------------------------------------


class HubSession:
    def __init__(self, ws: ServerConnection, sealer: Sealer, opener: Opener, hello: dict[str, Any]):
        self.ws = ws
        self.sealer = sealer
        self.opener = opener
        self.hello = hello
        self.frames: list[dict[str, Any]] = []
        self.arrived = asyncio.Event()
        self.used: set[int] = set()
        self.closed = asyncio.Event()

    def received(self, frame: dict[str, Any]) -> None:
        self.frames.append(frame)
        self.arrived.set()

    async def send(self, obj: dict[str, Any]) -> None:
        await self.ws.send(self.sealer.seal(obj))

    async def next(
        self, t: str, pred: Callable[[dict[str, Any]], bool] | None = None, timeout: float = 3.0
    ) -> dict[str, Any]:
        """The first frame of type ``t`` (matching ``pred``) not returned before."""

        async def find() -> dict[str, Any]:
            while True:
                for i, f in enumerate(self.frames):
                    if i not in self.used and f.get("t") == t and (pred is None or pred(f)):
                        self.used.add(i)
                        return f
                self.arrived.clear()
                await self.arrived.wait()

        return await asyncio.wait_for(find(), timeout)


@dataclass
class FakeHub:
    """A hub speaking handshake v2: ``refuse`` closes after ``hello`` with that close code;
    ``wrong_home`` signs the welcome with another key (a hub posing as the home)."""

    node_pub: bytes | None = None
    home_key: bytes | None = None
    home_static: crypto.KeyPair = field(default_factory=crypto.generate_keypair)
    refuse_code: int = 4003
    wrong_home: bool = False
    settings: dict[str, Any] = field(
        default_factory=lambda: {"log_level": "info", "metrics_every": 0.1}
    )
    refuse: str | None = None  #: the close reason when refusing
    sessions: list[HubSession] = field(default_factory=list)
    hellos: list[dict[str, Any]] = field(default_factory=list)
    refused: int = 0
    on_frame: Callable[[HubSession, dict[str, Any]], Coroutine[Any, Any, None]] | None = None
    new_session: asyncio.Event = field(default_factory=asyncio.Event)
    server: Server | None = None
    port: int = 0

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self.port}"

    async def handler(self, ws: ServerConnection) -> None:
        if ws.request is None or ws.request.path != "/api/cluster/agent":
            await ws.close(1008, "wrong path")
            return
        hello_text = await ws.recv()
        assert isinstance(hello_text, str)
        hello = json.loads(hello_text)
        self.hellos.append(hello)
        if self.refuse:
            self.refused += 1
            await ws.close(self.refuse_code, self.refuse)
            return
        server_nonce = os.urandom(16)
        eph = crypto.generate_keypair()
        challenge_text = json.dumps(
            {"t": "challenge", "server_eph": eph.public_b64, "nonce": crypto.b64e(server_nonce)}
        )
        await ws.send(challenge_text)
        client_eph = crypto.b64d(hello["eph"])
        if hello["kind"] == "node":
            assert self.node_pub is not None
            ikm = (
                dh(eph.private, client_eph)
                + dh(eph.private, self.node_pub)
                + dh(self.home_static.private, client_eph)
            )
        else:
            assert self.home_key is not None
            ikm = self.home_key + dh(eph.private, client_eph)
        th = transcript_hash(hello_text.encode(), challenge_text.encode())
        keys = derive_keys(ikm, crypto.b64d(hello["nonce"]), server_nonce)
        auth = json.loads(await ws.recv())
        if crypto.b64d(auth["proof"]) != client_proof(keys.c2s, th):
            self.refused += 1
            await ws.close(4004, "auth_failed")
            return
        sess = HubSession(ws, Sealer(keys.s2c), Opener(keys.c2s), hello)
        signer = bytes(32) if self.wrong_home else keys.s2c
        await sess.send(
            {
                "t": "welcome",
                "session": f"s{len(self.sessions) + 1}",
                "settings": self.settings,
                "proof": crypto.b64e(server_proof(signer, th)),
            }
        )
        self.sessions.append(sess)
        self.new_session.set()
        try:
            async for raw in ws:
                assert isinstance(raw, bytes)
                frame = sess.opener.open(raw)
                sess.received(frame)
                if self.on_frame is not None:
                    asyncio.create_task(self.on_frame(sess, frame))
        except Exception:  # noqa: BLE001, S110 - the agent closed it
            pass
        finally:
            sess.closed.set()

    async def wait_session(self, n: int, timeout: float = 5.0) -> HubSession:
        async def wait() -> HubSession:
            while len(self.sessions) < n:
                self.new_session.clear()
                await self.new_session.wait()
            return self.sessions[n - 1]

        return await asyncio.wait_for(wait(), timeout)


@contextlib.asynccontextmanager
async def running_hub(hub: FakeHub) -> AsyncIterator[FakeHub]:
    async with serve(hub.handler, "127.0.0.1", 0) as server:
        hub.server = server
        hub.port = next(iter(server.sockets)).getsockname()[1]
        yield hub


# --- the LLM runtime -------------------------------------------------------------------------


@dataclass
class FakeRuntimeState:
    """An Ollama-like runtime: models from ``/api/tags`` and ``/api/ps``, answers on its
    OpenAI-compatible ``/v1`` endpoints. ``chunked`` streams with chunked transfer
    encoding (else read to close); ``usage`` off: a runtime that reports none."""

    models: list[str] = field(default_factory=lambda: ["llama3:latest", "nomic-embed-text:latest"])
    loaded: list[str] = field(default_factory=lambda: ["llama3:latest"])
    pieces: list[str] = field(default_factory=lambda: ["Hel", "lo", "!"])
    delay: float = 0.0
    chunked: bool = True
    usage: bool = True
    requests: list[tuple[str, dict[str, Any]]] = field(default_factory=list)


def _handler(state: FakeRuntimeState) -> type[BaseHTTPRequestHandler]:
    class H(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_: Any) -> None:
            pass

        def _json(self, obj: Any, status: int = 200) -> None:
            body = json.dumps(obj).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(body)

        def _stream(self, events: list[bytes]) -> None:
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            if state.chunked:
                self.send_header("Transfer-Encoding", "chunked")
            self.send_header("Connection", "close")
            self.end_headers()
            for e in [*events, b"[DONE]"]:
                data = b"data: " + e + b"\n\n"
                if state.chunked:
                    data = f"{len(data):x}\r\n".encode() + data + b"\r\n"
                self.wfile.write(data)
                self.wfile.flush()
                if state.delay:
                    time.sleep(state.delay)
            if state.chunked:
                self.wfile.write(b"0\r\n\r\n")
            self.close_connection = True

        def do_GET(self) -> None:  # noqa: N802
            if self.path == "/api/tags":
                details = {"family": "llama", "quantization_level": "Q4_K_M"}
                self._json(
                    {"models": [{"name": m, "size": 100, "details": details}
                                for m in state.models]}
                )  # fmt: skip
            elif self.path == "/api/ps":
                self._json({"models": [{"name": m} for m in state.loaded]})
            elif self.path == "/v1/models":
                self._json({"data": [{"id": m} for m in state.models]})
            else:
                self._json({"error": "not found"}, 404)

        def do_POST(self) -> None:  # noqa: N802
            n = int(self.headers.get("Content-Length") or 0)
            body = json.loads(self.rfile.read(n) or b"{}")
            state.requests.append((self.path, body))
            if body.get("model") not in state.models:
                self._json({"error": {"message": f"model '{body.get('model')}' not found"}}, 404)
                return
            usage = {"prompt_tokens": 5, "completion_tokens": len(state.pieces)}
            if self.path in ("/v1/chat/completions", "/v1/completions"):
                chat = self.path == "/v1/chat/completions"

                def choice(p: str, whole: bool) -> dict[str, Any]:
                    if not chat:
                        return {"index": 0, "text": p}
                    return {"index": 0, ("message" if whole else "delta"): {"content": p}}

                if body.get("stream"):
                    events = [json.dumps({"choices": [choice(p, False)]}).encode()
                              for p in state.pieces]  # fmt: skip
                    if state.usage:
                        events.append(json.dumps({"choices": [], "usage": usage}).encode())
                    self._stream(events)
                else:
                    out: dict[str, Any] = {"choices": [choice("".join(state.pieces), True)]}
                    if state.usage:
                        out["usage"] = usage
                    self._json(out)
            elif self.path == "/v1/embeddings":
                inputs = body["input"] if isinstance(body["input"], list) else [body["input"]]
                self._json({"data": [{"index": i, "embedding": [float(i), 0.5]}
                                     for i in reversed(range(len(inputs)))],
                            "usage": {"prompt_tokens": 2}})  # fmt: skip
            else:
                self._json({"error": "not found"}, 404)

    return H


@contextlib.contextmanager
def fake_runtime(state: FakeRuntimeState | None = None) -> Iterator[tuple[str, FakeRuntimeState]]:
    st = state or FakeRuntimeState()
    srv = ThreadingHTTPServer(("127.0.0.1", 0), _handler(st))
    srv.daemon_threads = True
    t = threading.Thread(target=srv.serve_forever, args=(0.05,), daemon=True)
    t.start()
    try:
        yield f"http://127.0.0.1:{srv.server_address[1]}", st
    finally:
        srv.shutdown()
        srv.server_close()
