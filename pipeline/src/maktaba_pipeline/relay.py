"""The extension relay ("service door", story 31.R): a node runs an extension's stages
without ever holding its secret.

Since slot 0120 an extension's secret (the Plex token) is sealed in ``home_secrets``,
which only the home's API can open. A node's :class:`~.integrations.plex.PlexClient`
therefore talks through :class:`RelayTransport`: each HTTP exchange becomes an
``svc.request`` frame on the node's sealed agent channel; the home checks the machine and
the call against the extension's allow-list, adds the secret, forwards it to the server it
has configured and streams the answer back with the secret scrubbed out
(``api/internal/cluster/relay``). No URL ever leaves the node: only a method, a path and a
query, which the home matches exactly.

Settings: the rules section ``relay`` (:class:`RelayConfig`; ``enabled`` on by default).
Off, the home refuses every relayed call and the stages that need it stay on the home
(:func:`~.cluster.stages.lifted_placements`).
"""

from __future__ import annotations

import asyncio
import concurrent.futures
import contextlib
import time
import urllib.parse
from collections.abc import Coroutine, Mapping
from dataclasses import dataclass
from typing import Any, Protocol

from . import settings
from .log import get_logger

__all__ = [
    "RELAY_BASE",
    "RelayChannel",
    "RelayConfig",
    "RelayError",
    "RelayTransport",
    "attach",
    "attached",
    "detach",
    "enabled_in",
    "node_may_relay",
    "plex_client",
    "relayed",
]

#: the base URL a relayed client is built with (never dialled: only its path is sent)
RELAY_BASE = "relay://"
#: refusals worth another try after a pause (the home's per-machine limits)
RETRY_CODES = frozenset({"rate_limited", "busy"})
RETRY_DELAYS = (1.0, 2.0, 4.0)
#: how long a call waits for the channel to (re)connect
CONNECT_WAIT_SEC = 15.0

_log = get_logger()


@dataclass(frozen=True)
class RelayConfig:
    """The extension relay: nodes run the Plex stages through the home, which adds the
    Plex token they never see."""

    #: off: the home refuses every relayed call and the Plex stages run on the home only
    enabled: bool = True
    #: calls a minute each machine may make through the home
    requests_per_minute: int = 600
    #: calls a machine may make at once above that rate (a short burst)
    burst: int = 60
    #: calls of one machine running at the same time
    max_in_flight: int = 4
    #: the largest answer passed back (a Plex library listing can be large)
    max_response_mb: int = 256
    #: how long one call may take
    timeout_sec: int = 120


_R = "Extension relay"
settings.register(RelayConfig, {
    "enabled": settings.Help(_R, "Relay extension calls for machines",
                             "Machines run the Plex stages (index, collections) through the "
                             "home, which adds the Plex token; the token never leaves the home. "
                             "Off: those stages run on the home only."),
    "requests_per_minute": settings.Help(_R, "Calls a minute per machine", minimum=1,
                                         maximum=100_000),
    "burst": settings.Help(_R, "Burst per machine", "Calls above the rate a machine may make "
                           "at once.", minimum=1, maximum=10_000),
    "max_in_flight": settings.Help(_R, "Calls at once per machine", minimum=1, maximum=64),
    "max_response_mb": settings.Help(_R, "Largest answer (MB)", minimum=1, maximum=4096),
    "timeout_sec": settings.Help(_R, "Time limit per call (s)", minimum=1, maximum=3600),
})  # fmt: skip


class RelayError(Exception):
    """The home refused or could not complete a relayed call."""

    def __init__(self, code: str, detail: str = "") -> None:
        super().__init__(f"{code}: {detail}" if detail else code)
        self.code = code
        self.detail = detail


class RelayChannel(Protocol):
    """What the agent's channel offers (:class:`~.cluster.agent.client.ChannelAgent`)."""

    def relay_available(self) -> bool: ...

    def relay_request(
        self,
        ext: str,
        method: str,
        path: str,
        *,
        accept: str,
        content_type: str,
        body: bytes | None,
        timeout: float,
    ) -> Coroutine[Any, Any, tuple[int, str, bytes]]: ...


_CHANNEL: RelayChannel | None = None
_LOOP: asyncio.AbstractEventLoop | None = None


def attach(channel: RelayChannel, loop: asyncio.AbstractEventLoop | None = None) -> None:
    """The agent offers its channel to this process's relayed clients."""
    global _CHANNEL, _LOOP
    _CHANNEL = channel
    _LOOP = loop or asyncio.get_running_loop()


def detach() -> None:
    global _CHANNEL, _LOOP
    _CHANNEL, _LOOP = None, None


def attached() -> bool:
    return _CHANNEL is not None


def _agent_enabled() -> bool:
    from .cluster.agent import enabled

    return enabled()


def node_may_relay() -> bool:
    """A node may take relayed stages when its worker runs an agent (the channel)."""
    return attached() or _agent_enabled()


class RelayTransport:
    """A :class:`~.integrations.plex.Transport` that sends each exchange to the home.

    Synchronous like every Plex transport: the stages call the client in a thread
    (``asyncio.to_thread``), and the exchange runs on the agent's event loop. The
    ``X-Plex-Token`` header (empty on a node) is never sent; the home adds its own.
    """

    def __init__(
        self,
        ext: str,
        *,
        channel: RelayChannel | None = None,
        loop: asyncio.AbstractEventLoop | None = None,
        connect_wait: float = CONNECT_WAIT_SEC,
        delays: tuple[float, ...] = RETRY_DELAYS,
    ) -> None:
        self.ext = ext
        self._channel = channel
        self._loop = loop
        self.connect_wait = connect_wait
        self.delays = delays

    def _target(self, url: str) -> str:
        u = urllib.parse.urlsplit(url)
        path = u.path or "/"
        return f"{path}?{u.query}" if u.query else path

    def request(
        self,
        method: str,
        url: str,
        headers: Mapping[str, str],
        body: bytes | None,
        timeout: float,
    ) -> tuple[int, bytes]:
        from .integrations.plex import TransportError

        channel = self._channel or _CHANNEL
        loop = self._loop or _LOOP
        if channel is None or loop is None:
            raise TransportError("no channel to the home (the extension relay needs the agent)")
        with contextlib.suppress(RuntimeError):
            if asyncio.get_running_loop() is loop:
                raise TransportError("a relayed call must run in a worker thread")
        target = self._target(url)
        accept = next((v for k, v in headers.items() if k.lower() == "accept"), "")
        ctype = next((v for k, v in headers.items() if k.lower() == "content-type"), "")
        attempt = 0
        while True:
            self._wait_connected(channel)
            fut: concurrent.futures.Future[tuple[int, str, bytes]] = (
                asyncio.run_coroutine_threadsafe(
                    channel.relay_request(
                        self.ext,
                        method,
                        target,
                        accept=accept,
                        content_type=ctype,
                        body=body,
                        timeout=timeout,
                    ),
                    loop,
                )
            )
            try:
                status, _ctype, data = fut.result(timeout + 30.0)
            except RelayError as exc:
                if exc.code in RETRY_CODES and attempt < len(self.delays):
                    time.sleep(self.delays[attempt])
                    attempt += 1
                    continue
                if exc.code in ("not_allowed", "home_only", "unknown_extension", "bad_request"):
                    # a call the home will never relay: Plex's own "forbidden"
                    _log.warning("relay_call_refused", ext=self.ext, method=method,
                                 path=target.split("?", 1)[0], code=exc.code)  # fmt: skip
                    return 403, b""
                raise TransportError(f"relay: {exc}") from exc
            except TimeoutError as exc:
                fut.cancel()
                raise TransportError("relay: no answer from the home") from exc
            except ConnectionError as exc:
                raise TransportError(f"relay: {exc}") from exc
            return status, data

    def _wait_connected(self, channel: RelayChannel) -> None:
        deadline = time.monotonic() + self.connect_wait
        while not channel.relay_available():
            if time.monotonic() >= deadline:
                from .integrations.plex import TransportError

                raise TransportError("no channel to the home (the agent is not connected)")
            time.sleep(0.2)


def plex_client(settings: Mapping[str, str]) -> Any:
    """The Plex client for these settings: direct with the token (the home), else through
    the home's door (a node, which has no token)."""
    from .integrations.plex import PlexClient

    token = settings.get("token", "")
    if token or not node_may_relay():
        return PlexClient(settings.get("url", ""), token)
    return PlexClient(RELAY_BASE + "plex", "", transport=RelayTransport("plex"))


def relayed(settings: Mapping[str, str]) -> bool:
    """Would :func:`plex_client` go through the home?"""
    return not settings.get("token") and node_may_relay()


async def enabled_in(db: Any) -> bool:
    """``rules.relay.enabled`` (unreadable settings: off, the stages stay home-only)."""
    from .rules import read

    try:
        cfg = (await read(db, "relay"))[0]
    except Exception:  # noqa: BLE001
        _log.warning("relay_settings_unreadable")
        return False
    return bool(getattr(cfg, "enabled", False))
