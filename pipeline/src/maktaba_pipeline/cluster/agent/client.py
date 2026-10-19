"""The agent's channel to the home (design-agent.md sections 4, 5, 8, 10).

:class:`ChannelAgent` dials ``ws(s)://<home api>/api/cluster/agent``, runs handshake v2
(both sides prove who they are; fresh ephemeral keys per connection), then keeps the
channel for as long as the worker runs:

- C→S ``metrics`` every ``metrics_every`` s (``welcome.settings``, default 2), ``logs``
  batches within 250 ms, ``job`` events as they happen, ``dropped`` after a reconnect,
  ``llm.models`` after ``welcome`` and on change, ``llm.request`` / ``llm.cancel`` for the
  door, ``llm.chunk`` / ``llm.done`` for requests the hub routed here, ``cmd.result``.
- S→C ``cmd`` (drain, undrain, reload_stages, log_level, disconnect), ``llm.request``,
  ``llm.cancel``, ``llm.chunk`` / ``llm.done`` answering this process's requests.
- ``ping`` / ``pong`` every 15 s both ways; 45 s without a frame closes the channel.

Reconnects: 1 s doubling to 30 s, ±20 % jitter, reset once a session lasted 60 s; the
close codes of section 4.6 decide the rest (``4010 revoked``: no reconnect, the worker
stops). Nothing here ever blocks or fails the worker: collection runs in threads, files
are written by their own thread, and every error is logged (``warning`` at most once a
minute per kind) and retried.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import platform
import random
import time
import urllib.parse
import uuid
from collections import deque
from collections.abc import AsyncIterator, Awaitable, Callable, Mapping
from dataclasses import dataclass, field
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from ... import __version__
from ...llm.errors import ClusterUnavailable, LLMError, error_for
from ...llm.executor import Execution
from ...llm.service import LocalLLM
from ...log import get_logger
from .. import crypto
from .codec import (
    PROTOCOL_VERSION,
    FrameError,
    Opener,
    Sealer,
    client_proof,
    derive_keys,
    dumps,
    home_ikm,
    node_ikm,
    server_proof,
    transcript_hash,
)
from .files import ClusterFiles
from .logship import AGENT_LOGGER, LogShipper
from .metrics import MetricsCollector

__all__ = [
    "AGENT_PATH",
    "AgentIdentity",
    "ChannelAgent",
    "Commands",
    "Refused",
    "agent_url",
]

AGENT_PATH = "/api/cluster/agent"
PING_SEC = 15.0
SILENCE_SEC = 45.0
LOGS_EVERY_SEC = 0.25
LLM_RECHECK_SEC = 30.0
HANDSHAKE_SEC = 10.0
BACKOFF_MIN_SEC = 1.0
BACKOFF_MAX_SEC = 30.0
#: a session that lasted this long resets the reconnect delay
STABLE_SEC = 60.0
JOB_BUFFER = 1_000
LOG_BATCH = 1_000  #: records per ``logs`` frame (section 4.7)
LOG_BATCH_BYTES = 3 * 1024 * 1024
MAX_FRAME = 4 * 1024 * 1024  #: a sealed frame's limit (section 4.7)

#: close codes after which the agent waits the longest delay (section 4.6)
LONG_WAIT_CODES = frozenset({4001, 4003, 4004, 4009, 4013})
CLOSE_REVOKED = 4010
CLOSE_NOT_MEMBER = 4003
CLOSE_NAMES = {
    1000: "normal",
    1001: "going_away",
    4000: "protocol_error",
    4001: "unsupported_version",
    4003: "not_member",
    4004: "auth_failed",
    4008: "timeout",
    4009: "replaced",
    4010: "revoked",
    4011: "seal_error",
    4012: "too_large",
    4013: "rate_limited",
    4014: "disconnect",
}

_log = get_logger(logger=AGENT_LOGGER)


class Refused(Exception):
    """The handshake failed on this side (no key, the hub's proof is wrong, ...)."""

    def __init__(self, message: str, *, long_wait: bool = True) -> None:
        super().__init__(message)
        self.long_wait = long_wait


class FrameTooLarge(ValueError):
    pass


def agent_url(home_url: str) -> str:
    """``http://home:8080`` -> ``ws://home:8080/api/cluster/agent`` (https -> wss)."""
    u = urllib.parse.urlsplit(home_url.strip() if "//" in home_url else "http://" + home_url)
    scheme = {"https": "wss", "wss": "wss"}.get(u.scheme, "ws")
    base = u.path.rstrip("/")
    if base.endswith("/api"):
        base = base[: -len("/api")]
    return urllib.parse.urlunsplit((scheme, u.netloc, base + AGENT_PATH, "", ""))


def _now() -> str:
    return datetime.now(UTC).isoformat(timespec="seconds").replace("+00:00", "Z")


@dataclass
class AgentIdentity:
    kind: str  #: ``node`` or ``home``
    url: str  #: the home's API base URL
    worker_id: str
    host: str
    role: str
    pid: int = field(default_factory=os.getpid)
    node_id: str | None = None
    name: str = ""
    #: a node's X25519 pairing key (32 bytes)
    node_private: bytes | None = None
    #: a node's copy of the home's cluster public key (``home.cluster_pub``)
    home_static: bytes | None = None
    #: a home process's key file (read at every connect: the API rewrites it at start)
    home_key_path: Path | None = None
    #: the optional, informational hello fields (section 4.2)
    ip: str | None = None
    os: str | None = None
    version: str | None = None

    @classmethod
    def describe_os(cls) -> tuple[str, str]:
        return platform.system().lower(), __version__

    def hello(self, eph_public: bytes, nonce: bytes) -> dict[str, Any]:
        out: dict[str, Any] = {
            "t": "hello",
            "v": PROTOCOL_VERSION,
            "kind": self.kind,
            "worker_id": self.worker_id,
            "pid": self.pid,
            "host": self.host,
            "eph": crypto.b64e(eph_public),
            "nonce": crypto.b64e(nonce),
        }
        if self.node_id:
            out["node_id"] = self.node_id
        for key in ("ip", "os", "version"):
            value = getattr(self, key)
            if value:
                out[key] = value
        return out

    def can_connect(self) -> str | None:
        """None, or why this process cannot have a channel at all."""
        if self.kind == "node":
            if self.node_private is None:
                return "no pairing key in the node config"
            if self.home_static is None:
                return (
                    "the node config has no home.cluster_pub: re-join this machine"
                    " (maktaba-pipeline join --rejoin)"
                )
        return None


@dataclass
class Commands:
    """What ``cmd`` frames act on (the worker's stage selection, its shutdown)."""

    drain: Callable[[bool], Awaitable[str]] | None = None
    reload_stages: Callable[[], Awaitable[str]] | None = None
    #: the home refused this machine for good (revoked, not a member): the worker
    #: stops gracefully (exit code 77); called with the reason
    revoked: Callable[[str], None] | None = None


class _Session:
    def __init__(self, ws: Any, sealer: Sealer, opener: Opener, session_id: str) -> None:
        self.ws = ws
        self.sealer = sealer
        self.opener = opener
        self.id = session_id
        self.lock = asyncio.Lock()
        self.started = time.monotonic()
        self.last_rx = time.monotonic()
        self.closed = False
        self.tasks: set[asyncio.Task[Any]] = set()
        self.llm_tasks: dict[str, asyncio.Task[Any]] = {}

    async def send(self, obj: Mapping[str, Any]) -> None:
        if self.closed:
            raise ConnectionError("session closed")
        plain = dumps(obj)
        if len(plain) + 16 > MAX_FRAME:
            raise FrameTooLarge(f"a {obj.get('t')} frame of {len(plain)} bytes")
        async with self.lock:
            await self.ws.send(self.sealer.seal_bytes(plain))

    def spawn(self, coro: Awaitable[Any]) -> asyncio.Task[Any]:
        async def run() -> Any:
            return await coro

        task = asyncio.create_task(run())
        self.tasks.add(task)
        task.add_done_callback(self.tasks.discard)
        return task


class ChannelAgent:
    def __init__(
        self,
        identity: AgentIdentity,
        *,
        shipper: LogShipper,
        collector: MetricsCollector | None = None,
        files: ClusterFiles | None = None,
        llm: LocalLLM | None = None,
        commands: Commands | None = None,
        on_settings: Callable[[Mapping[str, Any]], None] | None = None,
        metrics_every: float = 2.0,
        status_path: Path | None = None,
        backoff_min: float = BACKOFF_MIN_SEC,
        backoff_max: float = BACKOFF_MAX_SEC,
        stable_sec: float = STABLE_SEC,
        ping_sec: float = PING_SEC,
        silence_sec: float = SILENCE_SEC,
        llm_recheck_sec: float = LLM_RECHECK_SEC,
        rng: random.Random | None = None,
    ) -> None:
        self.identity = identity
        self.shipper = shipper
        self.collector = collector
        self.files = files
        self.llm = llm
        self.commands = commands if commands is not None else Commands()
        self.on_settings = on_settings
        self.metrics_every = metrics_every
        self.status_path = status_path
        self.backoff_min = backoff_min
        self.backoff_max = backoff_max
        self.stable_sec = stable_sec
        self.ping_sec = ping_sec
        self.silence_sec = silence_sec
        self.llm_recheck_sec = llm_recheck_sec
        self.rng = rng or random.Random()
        self.state = "idle"
        self.session_id: str | None = None
        self.connected_since: str | None = None
        self.last_error: str | None = None
        self.last_close: tuple[int, str] | None = None
        self.connects = 0
        self.settings: dict[str, Any] = {}
        self.revoked = False
        self._session: _Session | None = None
        self._jobs: deque[dict[str, Any]] = deque(maxlen=JOB_BUFFER)
        self._pending: dict[str, asyncio.Queue[dict[str, Any] | None]] = {}
        self._stop = asyncio.Event()
        self._no_reconnect = False
        self._last_warn: dict[str, float] = {}
        self._next_delay: float | None = None
        self._latest_metrics: dict[str, Any] | None = None
        #: the handshake's fresh randomness (tests pin it to the interop vectors)
        self._new_eph: Callable[[], crypto.KeyPair] = crypto.generate_keypair
        self._new_nonce: Callable[[], bytes] = lambda: os.urandom(16)
        self._metrics_task: asyncio.Task[None] | None = None

    # -- public --------------------------------------------------------------------

    @property
    def connected(self) -> bool:
        return self._session is not None and not self._session.closed

    def stop(self) -> None:
        """End :meth:`run`: the session closes (1000 normal), nothing reconnects."""
        self._stop.set()

    def job_sink(self, event: dict[str, Any]) -> None:
        """A job tracker sink: for the live channel only (events of a time without a
        channel are in the files, not resent: section 5.5)."""
        if self.connected:
            self._jobs.append(event)

    async def run(self, shutdown: asyncio.Event | None = None) -> None:
        """Keep the channel and collect metrics until :meth:`stop` or cancellation.

        ``shutdown`` is the worker's: when it is set (the worker starts draining) the last
        metrics frame goes out (``final``: the health counters' shutdown values) and the
        regular frames stop; the channel stays open for the job events of the draining
        jobs until the worker cancels this task."""
        self._metrics_task = asyncio.create_task(self._metrics_loop(), name="agent-metrics")
        watcher = None
        if shutdown is not None:
            watcher = asyncio.create_task(self._watch(shutdown), name="agent-shutdown")
        side = [
            self._metrics_task,
            asyncio.create_task(self._llm_loop(), name="agent-llm-models"),
        ]
        try:
            why_not = self.identity.can_connect()
            if why_not is None:
                await self._connect_loop()
            else:
                self.last_error = why_not
                _log.error("cluster_agent_no_channel", reason=why_not)
                self._set_state("no_channel")
                await self._stop.wait()
        finally:
            for t in side + ([watcher] if watcher else []):
                t.cancel()
            for t in side + ([watcher] if watcher else []):
                with contextlib.suppress(asyncio.CancelledError, Exception):
                    await t
            self._set_state("stopped")

    async def _watch(self, shutdown: asyncio.Event) -> None:
        await shutdown.wait()
        if self._metrics_task is not None:
            self._metrics_task.cancel()
        await self.send_final_metrics()

    async def send_final_metrics(self) -> None:
        """The last metrics frame at a graceful stop (it carries the health counters'
        final values), to the channel and the file."""
        if self.collector is None:
            return
        try:
            frame = await asyncio.wait_for(asyncio.to_thread(self.collector.collect, final=True), 5)
        except Exception as exc:  # noqa: BLE001
            self._warn("cluster_agent_metrics_failed", error=str(exc))
            return
        self._record_metrics(frame)
        s = self._session
        if s is not None and not s.closed:
            with contextlib.suppress(Exception):
                await asyncio.wait_for(s.send(frame), 2)

    # -- the connection --------------------------------------------------------------

    async def _connect_loop(self) -> None:
        attempt = 0
        while not self._stop.is_set():
            self._set_state("connecting")
            long_wait = False
            lasted = 0.0
            try:
                lasted = await self._connect_once()
            except Refused as exc:
                self.last_error = str(exc)
                long_wait = exc.long_wait
                self._warn("cluster_agent_refused", reason=str(exc))
            except (OSError, TimeoutError, asyncio.TimeoutError, FrameError) as exc:  # noqa: UP041
                self.last_error = f"{type(exc).__name__}: {exc}"
                self._warn("cluster_agent_unreachable", error=self.last_error)
            except Exception as exc:  # noqa: BLE001 - never kill the worker
                self.last_error = f"{type(exc).__name__}: {exc}"
                if type(exc).__module__.startswith("websockets"):
                    self._warn("cluster_agent_connection_lost", error=self.last_error)
                else:
                    _log.exception("cluster_agent_error")
            code = self.last_close[0] if self.last_close else None
            self.last_close = None
            if code == CLOSE_REVOKED:
                self._revoked("closed by the home (4010 revoked)")
            elif code == CLOSE_NOT_MEMBER and self.identity.kind == "node":
                self._revoked("the home does not know this machine (4003 not_member)")
            if self._stop.is_set() or self._no_reconnect:
                return
            if code == 4009:
                _log.error("cluster_agent_replaced", worker_id=self.identity.worker_id)
            if code in LONG_WAIT_CODES:
                long_wait = True
            attempt = 1 if lasted >= self.stable_sec else attempt + 1
            if self._next_delay is not None:
                delay, self._next_delay = self._next_delay, None
            elif long_wait:
                delay = self.backoff_max
            else:
                delay = self._backoff(attempt)
            self._set_state("backoff")
            with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                await asyncio.wait_for(self._stop.wait(), timeout=delay)

    def _backoff(self, attempt: int) -> float:
        """``backoff_min`` doubling to ``backoff_max``, ±20 % jitter (never above the max)."""
        base = min(self.backoff_max, self.backoff_min * (2 ** max(0, attempt - 1)))
        return float(min(self.backoff_max, base * self.rng.uniform(0.8, 1.2)))

    def _note_close(self, ws: Any) -> None:
        code = getattr(ws, "close_code", None)
        if code is not None:
            reason = str(getattr(ws, "close_reason", "") or "")
            self.last_close = (int(code), reason)
            if code not in (1000, 1001, 1005, 1006):
                name = CLOSE_NAMES.get(int(code), str(code))
                self.last_error = f"closed {code} {reason or name}".strip()

    async def _connect_once(self) -> float:
        """One connection; returns how long its session lasted (0 without one)."""
        from websockets.asyncio.client import connect

        url = agent_url(self.identity.url)
        async with connect(
            url,
            open_timeout=HANDSHAKE_SEC,
            ping_interval=None,  # liveness is the protocol's ping/pong (section 4.5)
            close_timeout=2,
            max_size=MAX_FRAME,
            max_queue=64,
        ) as ws:
            try:
                session = await asyncio.wait_for(self._handshake(ws), HANDSHAKE_SEC)
            except BaseException:
                self._note_close(ws)
                raise
            self._session = session
            self.connects += 1
            self.connected_since = _now()
            self._set_state("connected")
            _log.info("cluster_agent_connected", url=url, session=session.id)
            try:
                await self._serve(session)
            finally:
                session.closed = True
                self._session = None
                for t in list(session.tasks):
                    t.cancel()
                for q in self._pending.values():
                    q.put_nowait(None)  # the channel is gone
                self._jobs.clear()
                self._note_close(ws)
                _log.info("cluster_agent_disconnected", session=session.id,
                          close=self.last_close)  # fmt: skip
            return time.monotonic() - session.started

    async def _handshake(self, ws: Any) -> _Session:
        ident = self.identity
        home_key = self._home_key() if ident.kind == "home" else None
        eph = self._new_eph()
        client_nonce = self._new_nonce()
        hello_bytes = dumps(ident.hello(eph.public, client_nonce))
        await ws.send(hello_bytes.decode("utf-8"))
        raw = await ws.recv()
        if not isinstance(raw, str):
            raise FrameError("binary frame before authentication")
        challenge_bytes = raw.encode("utf-8")
        challenge = _json_object(raw)
        if challenge.get("t") != "challenge":
            raise Refused(str(challenge.get("reason") or challenge.get("error") or challenge))
        try:
            server_nonce = crypto.b64d(str(challenge["nonce"]))
            server_eph = crypto.b64d(str(challenge["server_eph"]))
            if ident.kind == "node":
                assert ident.node_private is not None and ident.home_static is not None
                ikm = node_ikm(eph.private, ident.node_private, server_eph, ident.home_static)
            else:
                assert home_key is not None
                ikm = home_ikm(home_key, eph.private, server_eph)
        except (KeyError, ValueError) as exc:
            raise FrameError(f"bad challenge: {exc}") from exc
        th = transcript_hash(hello_bytes, challenge_bytes)
        keys = derive_keys(ikm, client_nonce, server_nonce)
        auth = {"t": "auth", "proof": crypto.b64e(client_proof(keys.c2s, th))}
        await ws.send(dumps(auth).decode("utf-8"))
        raw = await ws.recv()
        if isinstance(raw, str):
            msg = _json_object(raw)
            raise Refused(str(msg.get("reason") or msg.get("error") or msg), long_wait=True)
        opener = Opener(keys.s2c)
        try:
            welcome = opener.open(raw)
            ok = welcome.get("t") == "welcome" and crypto.b64d(
                str(welcome.get("proof") or "")
            ) == server_proof(keys.s2c, th)
        except (FrameError, ValueError):
            ok = False
        if not ok:
            _log.error("cluster_agent_wrong_home", reason="this is not the home I joined")
            await ws.close(4004, "auth_failed")
            raise Refused("this is not the home I joined (its proof is wrong)")
        self.session_id = str(welcome.get("session") or "")
        self._apply_settings(welcome.get("settings") or {})
        return _Session(ws, Sealer(keys.c2s), opener, self.session_id)

    def _home_key(self) -> bytes:
        path = self.identity.home_key_path
        if path is None:
            raise Refused("no home agent key path")
        try:
            key = path.read_bytes()
        except FileNotFoundError as exc:
            raise Refused(f"home agent key {path} not found (is the home API running?)") from exc
        except OSError as exc:
            raise Refused(f"home agent key {path}: {exc}") from exc
        if len(key) != 32:
            try:
                key = crypto.b64d(key.decode("ascii").strip())
            except (ValueError, UnicodeDecodeError) as exc:
                raise Refused(f"home agent key {path} is not 32 bytes") from exc
        if len(key) != 32:
            raise Refused(f"home agent key {path} is not 32 bytes")
        return key

    def _apply_settings(self, settings: Mapping[str, Any]) -> None:
        self.settings = dict(settings)
        level = settings.get("log_level")
        if isinstance(level, str):
            self.shipper.set_level(level)
        every = settings.get("metrics_every")
        if isinstance(every, int | float) and 0.05 <= float(every) <= 600:
            self.metrics_every = float(every)
        if self.on_settings is not None:
            try:
                self.on_settings(self.settings)
            except Exception:  # noqa: BLE001
                _log.exception("cluster_agent_settings_failed")

    # -- a live session ----------------------------------------------------------------

    async def _serve(self, s: _Session) -> None:
        await self._on_connect(s)
        stopper = asyncio.create_task(self._stop.wait(), name="agent-stop")
        tasks = [
            asyncio.create_task(self._reader(s), name="agent-reader"),
            asyncio.create_task(self._pinger(s), name="agent-pinger"),
            asyncio.create_task(self._flusher(s), name="agent-flusher"),
            stopper,
        ]
        try:
            done, _ = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
            if stopper in done:
                with contextlib.suppress(Exception):
                    await self._flush(s)  # what is buffered goes out first
                await s.ws.close(1000, "normal")
                return
            for t in done:
                exc = t.exception()
                if exc is not None:
                    raise exc
        finally:
            for t in tasks:
                t.cancel()
            for t in tasks:
                with contextlib.suppress(asyncio.CancelledError, Exception):
                    await t

    async def _on_connect(self, s: _Session) -> None:
        latest = self._latest_metrics
        if latest is not None:
            await s.send(latest)
        if self.llm is not None:
            await s.send(self.llm.runtimes.frame())
        await self._send_dropped(s)

    async def _send_dropped(self, s: _Session) -> None:
        dropped, since = self.shipper.take_dropped_span()
        if dropped:
            await s.send({"t": "dropped", "logs": dropped, "since": since, "until": _now()})

    async def _reader(self, s: _Session) -> None:
        async for raw in s.ws:
            if isinstance(raw, str):
                await s.ws.close(4000, "protocol_error text frame")
                raise FrameError("text frame after authentication")
            try:
                frame = s.opener.open(raw)
            except FrameError:
                await s.ws.close(4011, "seal_error")
                raise
            s.last_rx = time.monotonic()
            await self._dispatch(s, frame)

    async def _dispatch(self, s: _Session, frame: dict[str, Any]) -> None:
        t = frame.get("t")
        if t == "ping":
            await s.send({"t": "pong", "ts": frame.get("ts")})
        elif t == "pong":
            return
        elif t == "cmd":
            self._guarded(s, self._command(s, frame))
        elif t == "llm.request":
            rid = str(frame.get("id"))
            s.llm_tasks[rid] = self._guarded(s, self._serve_llm(s, frame))
        elif t == "llm.cancel":
            task = s.llm_tasks.get(str(frame.get("id")))
            if task is not None:
                task.cancel()
        elif t in ("llm.chunk", "llm.done"):
            q = self._pending.get(str(frame.get("id")))
            if q is not None:
                q.put_nowait(frame)
        else:
            _log.debug("cluster_agent_unknown_frame", t=t)  # ignored (section 4.7)

    def _guarded(self, s: _Session, coro: Awaitable[None]) -> asyncio.Task[Any]:
        async def guarded() -> None:
            try:
                await coro
            except (asyncio.CancelledError, ConnectionError):
                raise
            except Exception:  # noqa: BLE001
                if not s.closed:
                    _log.exception("cluster_agent_handler_failed")

        return s.spawn(guarded())

    async def _pinger(self, s: _Session) -> None:
        while True:
            await asyncio.sleep(self.ping_sec)
            if time.monotonic() - s.last_rx > self.silence_sec:
                self._warn("cluster_agent_silent_home", seconds=round(self.silence_sec))
                await s.ws.close(4008, "timeout")
                return
            await s.send({"t": "ping", "ts": _now()})

    async def _flusher(self, s: _Session) -> None:
        while True:
            await self._flush(s)
            await asyncio.sleep(LOGS_EVERY_SEC)

    async def _flush(self, s: _Session) -> None:
        while self._jobs:
            await s.send(self._jobs.popleft())
        for _ in range(20):  # then yield: the next flush comes in 250 ms
            batch = self.shipper.take(LOG_BATCH)
            if not batch:
                break
            size, cut = 0, len(batch)
            for i, rec in enumerate(batch):
                size += len(dumps(rec))
                if size > LOG_BATCH_BYTES and i > 0:
                    cut = i  # keep the frame well under 4 MiB
                    break
            if cut < len(batch):
                self.shipper.put_back(batch[cut:])
                batch = batch[:cut]
            try:
                await s.send({"t": "logs", "records": batch})
            except BaseException:
                self.shipper.put_back(batch)
                raise
            if not len(self.shipper):
                break
        await self._send_dropped(s)

    # -- commands ------------------------------------------------------------------------

    async def _command(self, s: _Session, frame: Mapping[str, Any]) -> None:
        cmd_id = frame.get("id")
        name = str(frame.get("name") or "")
        args = frame.get("args") if isinstance(frame.get("args"), Mapping) else {}
        assert isinstance(args, Mapping)
        ok, detail = True, ""
        close: tuple[int, str] | None = None
        try:
            if name in ("drain", "undrain"):
                if self.commands.drain is None:
                    ok, detail = False, "this process has no stage selection"
                else:
                    detail = await self.commands.drain(name == "drain")
            elif name == "reload_stages":
                if self.commands.reload_stages is None:
                    ok, detail = False, "this process has no stage selection"
                else:
                    detail = await self.commands.reload_stages()
            elif name == "log_level":
                level = str(args.get("level") or "")
                if level.lower() not in ("debug", "info", "warning", "warn", "error"):
                    ok, detail = False, f"unknown level {level!r}"
                else:
                    detail = self.shipper.set_level(level)
            elif name == "disconnect":
                reason = str(args.get("reason") or "")
                if reason == "revoked":
                    detail = "revoked: stopping, not reconnecting"
                    self._revoked("the home sent disconnect (revoked)")
                else:
                    detail = "disconnecting; reconnecting later"
                    self._next_delay = self._backoff(1)
                close = (1000, "normal")
            else:
                ok, detail = False, f"unknown command {name!r}"
        except Exception as exc:  # noqa: BLE001 - reported to the hub
            ok, detail = False, f"{type(exc).__name__}: {exc}"[:500]
        _log.info("cluster_agent_command", name=name, ok=ok, detail=detail)
        await s.send({"t": "cmd.result", "id": cmd_id, "ok": ok, "detail": detail})
        if close is not None:
            await s.ws.close(*close)

    def _revoked(self, why: str) -> None:
        if self.revoked:
            return
        self.revoked = True
        self._no_reconnect = True
        self.last_error = why
        _log.error("cluster_agent_revoked", reason=why)
        if self.commands.revoked is not None:
            self.commands.revoked(why)

    # -- LLM: requests the hub routes here -------------------------------------------------

    async def _serve_llm(self, s: _Session, frame: Mapping[str, Any]) -> None:
        req_id = str(frame.get("id"))
        run = Execution()
        error: dict[str, Any] | None = None
        try:
            if self.llm is None:
                raise error_for("no_model: this process runs no LLM")
            body = frame.get("body")
            async for data in self.llm.run(
                str(frame.get("op") or "chat"),
                str(frame.get("model") or ""),
                body if isinstance(body, Mapping) else {},
                stream=bool(frame.get("stream", False)),
                wait=False,
                run=run,
                req_id=req_id,
                origin=str(frame.get("from") or "hub"),
            ):
                await s.send({"t": "llm.chunk", "id": req_id, "data": data})
        except asyncio.CancelledError:
            error = {"code": "cancelled", "detail": "cancelled by the requester"}
        except FrameTooLarge as exc:
            error = {"code": "too_large", "detail": str(exc)}
        except LLMError as exc:
            error = {"code": exc.code, "detail": str(exc)}
        except (ValueError, OSError, TimeoutError) as exc:
            error = {"code": "runtime_error", "detail": f"{type(exc).__name__}: {exc}"[:500]}
        finally:
            s.llm_tasks.pop(req_id, None)
        done: dict[str, Any] = {"t": "llm.done", "id": req_id, "usage": dict(run.usage)}
        if error is not None:
            done["error"] = error
        if not s.closed:
            await s.send(done)

    # -- LLM: this process asks the cluster (the door's transport) ----------------------------

    def llm_available(self) -> bool:
        return self.connected

    async def llm_request(
        self,
        op: str,
        model: str,
        body: Mapping[str, Any],
        *,
        stream: bool,
        run: Execution,
    ) -> AsyncIterator[dict[str, Any]]:
        s = self._session
        if s is None or s.closed:
            raise ClusterUnavailable("no channel to the home")
        req_id = uuid.uuid4().hex
        q: asyncio.Queue[dict[str, Any] | None] = asyncio.Queue()
        self._pending[req_id] = q
        finished = False
        try:
            await s.send(
                {
                    "t": "llm.request",
                    "id": req_id,
                    "model": model,
                    "op": op,
                    "body": dict(body),
                    "stream": stream,
                }
            )
            while True:
                item = await q.get()
                if item is None:
                    finished = True
                    raise ClusterUnavailable("the channel to the home closed mid-request")
                if item.get("t") == "llm.chunk":
                    data = item.get("data")
                    if isinstance(data, dict):
                        yield data
                    continue
                finished = True
                usage = item.get("usage")
                if isinstance(usage, Mapping):
                    run.usage.update({str(k): int(v or 0) for k, v in usage.items()})
                err = item.get("error")
                if err:
                    if isinstance(err, Mapping):
                        code, detail = str(err.get("code") or "error"), str(err.get("detail") or "")
                        raise error_for(f"{code}: {detail}" if detail else code)
                    raise error_for(str(err))
                return
        finally:
            self._pending.pop(req_id, None)
            if not finished and not s.closed:
                # the caller gave up: tell the hub, which tells the runner (section 5.1)
                s.spawn(_quiet(s.send({"t": "llm.cancel", "id": req_id})))

    # -- side loops --------------------------------------------------------------------------

    def _record_metrics(self, frame: dict[str, Any]) -> None:
        self._latest_metrics = frame
        if self.files is not None:
            self.files.write("metrics", {k: v for k, v in frame.items() if k != "t"})

    async def _metrics_loop(self) -> None:
        if self.collector is None:
            return
        while True:
            started = time.monotonic()
            try:
                frame = await asyncio.to_thread(self.collector.collect)
                self._record_metrics(frame)
                s = self._session
                if s is not None and not s.closed:
                    with contextlib.suppress(Exception):
                        await s.send(frame)
            except Exception as exc:  # noqa: BLE001
                self._warn("cluster_agent_metrics_failed", error=str(exc))
            elapsed = time.monotonic() - started
            await asyncio.sleep(max(0.02, self.metrics_every - elapsed))

    async def _llm_loop(self) -> None:
        if self.llm is None:
            return
        while True:
            try:
                changed = await self.llm.runtimes.refresh()
                s = self._session
                if changed:
                    _log.info(
                        "cluster_agent_llm_models",
                        runtimes=[r.base_url for r in self.llm.runtimes.runtimes],
                        models=sum(len(r.models) for r in self.llm.runtimes.runtimes),
                    )
                    if s is not None and not s.closed:
                        with contextlib.suppress(Exception):
                            await s.send(self.llm.runtimes.frame())
            except Exception as exc:  # noqa: BLE001
                self._warn("cluster_agent_llm_detect_failed", error=str(exc))
            await asyncio.sleep(self.llm_recheck_sec)

    # -- status ------------------------------------------------------------------------------

    def status(self) -> dict[str, Any]:
        return {
            "worker_id": self.identity.worker_id,
            "pid": self.identity.pid,
            "kind": self.identity.kind,
            "role": self.identity.role,
            "url": agent_url(self.identity.url),
            "state": self.state,
            "session": self.session_id if self.connected else None,
            "connected_since": self.connected_since if self.connected else None,
            "connects": self.connects,
            "last_error": self.last_error,
            "log_level": self.shipper.level,
            "files": None if self.files is None else self.files.status(),
            "updated_at": _now(),
        }

    def _set_state(self, state: str) -> None:
        if state == self.state:
            return
        self.state = state
        if self.status_path is None:
            return
        try:
            self.status_path.parent.mkdir(parents=True, exist_ok=True)
            tmp = self.status_path.with_suffix(".tmp")
            tmp.write_text(json.dumps(self.status(), indent=2, default=str), encoding="utf-8")
            os.replace(tmp, self.status_path)
        except OSError:
            pass

    def _warn(self, event: str, **fields: Any) -> None:
        """Warn once a minute per kind (a home down for an hour is one line a minute)."""
        now = time.monotonic()
        if now - self._last_warn.get(event, float("-inf")) >= 60.0:
            self._last_warn[event] = now
            _log.warning(event, url=self.identity.url, **fields)
        else:
            _log.debug(event, **fields)


async def _quiet(coro: Awaitable[Any]) -> None:
    with contextlib.suppress(Exception):
        await coro


def _json_object(raw: str) -> dict[str, Any]:
    try:
        obj = json.loads(raw)
    except ValueError as exc:
        raise FrameError("handshake frame is not JSON") from exc
    if not isinstance(obj, dict):
        raise FrameError("handshake frame is not an object")
    return obj
