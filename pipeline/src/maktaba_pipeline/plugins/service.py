"""A first-party plugin's way to Plex (Epic 54 wave 8, story 54.35): through the home's
service door, which adds the token and scrubs it out of every answer, so the plugin holds
neither the token nor Plex's address.

A plugin's stage runs in the ``service`` runner, which has no network at all. Its Plex
client sends each HTTP exchange as a call: on the home, to the plugin API's
``trusted/service`` (the API hands it to the door); on a joined machine, to its worker
(``service_request``, an engine op), which forwards it through its agent channel to the
home's door, as the worker's own Plex stages there always did (:mod:`maktaba_pipeline.relay`).

The job's binding (its plugin API transport, the runner's event loop, which way to go) is a
context variable set for the job, so one module-level ``PlexService`` (and its index cache)
serves every job, as in-process. In-process nothing is bound and the client is the one the
worker always used (:func:`maktaba_pipeline.relay.plex_client`)."""

from __future__ import annotations

import asyncio
import base64
import contextlib
import functools
import urllib.parse
from collections.abc import Iterator, Mapping
from contextvars import ContextVar
from dataclasses import dataclass
from typing import Any

__all__ = [
    "SERVICE_URL",
    "ServiceTransport",
    "bind",
    "bound",
    "plex_client",
    "plugin_settings",
]

#: the base a plugin's client has for a service: not an address (the door knows the real one),
#: so a scheme of its own, as the relay's ``relay://<ext>``
SERVICE_URL = "service://{name}"


@dataclass(frozen=True)
class _Binding:
    db: Any
    loop: asyncio.AbstractEventLoop
    via: str  # "api" (the home's plugin API) | "worker" (a joined machine's worker)


_BOUND: ContextVar[_Binding | None] = ContextVar("maktaba_service_binding", default=None)


@contextlib.contextmanager
def bind(db: Any, via: str) -> Iterator[None]:
    """The job's way to the door, for the Plex calls its stage makes (in threads too:
    ``asyncio.to_thread`` copies the context)."""
    token = _BOUND.set(_Binding(db, asyncio.get_running_loop(), via))
    try:
        yield
    finally:
        _BOUND.reset(token)


def bound() -> bool:
    return _BOUND.get() is not None


class ServiceTransport:
    """A :class:`~maktaba_pipeline.integrations.plex.Transport` to the home's service door,
    synchronous like every Plex transport (the stages call the client in a thread)."""

    def __init__(self, service: str) -> None:
        self.service = service

    def request(
        self,
        method: str,
        url: str,
        headers: Mapping[str, str],
        body: bytes | None,
        timeout: float,
    ) -> tuple[int, bytes]:
        from ..integrations.plex import TransportError

        b = _BOUND.get()
        if b is None:
            raise TransportError("no plugin job is bound to the service door")
        with contextlib.suppress(RuntimeError):
            if asyncio.get_running_loop() is b.loop:
                raise TransportError("a service call must run in a worker thread")
        u = urllib.parse.urlsplit(url)
        target = (u.path or "/") + (f"?{u.query}" if u.query else "")
        req = {
            "service": self.service,
            "method": method,
            "target": target,
            "accept": next((v for k, v in headers.items() if k.lower() == "accept"), ""),
            "content_type": next(
                (v for k, v in headers.items() if k.lower() == "content-type"), ""
            ),
            "body": base64.b64encode(body or b"").decode(),
            "timeout_ms": int(timeout * 1000),
        }
        if b.via == "api":
            coro = b.db.transport.call("trusted/service", req)
        else:
            coro = b.db.engine_relay.ask("service_request", **req)
        fut = asyncio.run_coroutine_threadsafe(coro, b.loop)
        try:
            got = fut.result(timeout + 30.0)
        except TimeoutError as exc:
            fut.cancel()
            raise TransportError("service: no answer from the door") from exc
        except Exception as exc:  # noqa: BLE001 - a refusal or the link
            code = str(getattr(exc, "code", "") or "")
            if code == "plugin_service_path" or "plugin_service_path" in str(exc):
                # a call the door never forwards: Plex's own "forbidden", as through the relay
                return 403, b""
            raise TransportError(f"service: {exc}") from exc
        return int(got["status"]), base64.b64decode(str(got.get("body") or ""))


def plex_client(settings: Mapping[str, str]) -> Any:
    """The Plex client for a job: through the door when a plugin job is bound, else the
    worker's own (direct with the token on the home, the relay on a node)."""
    from .. import relay
    from ..integrations.plex import PlexClient

    if bound():
        return PlexClient(SERVICE_URL.format(name="plex"), "", transport=ServiceTransport("plex"))
    return relay.plex_client(settings)


@functools.cache
def _secret_fields() -> dict[str, frozenset[str]]:
    """Each extension's secret setting fields (read once: the manifests are the release's)."""
    from ..extensions import discover

    return {
        ext.manifest.name: frozenset(f.key for f in ext.manifest.settings if f.secret)
        for ext in discover()
    }


def plugin_settings(
    settings: Mapping[str, Mapping[str, str]], services: tuple[str, ...]
) -> dict[str, dict[str, str]]:
    """What a plugin's job gets of the extensions' settings: no secret field, and for each
    service the door reaches, no address (``service:<name>`` stands for it)."""
    secret = _secret_fields()
    out: dict[str, dict[str, str]] = {}
    for name, values in settings.items():
        keep = {k: str(v) for k, v in values.items() if k not in secret.get(name, set())}
        if name in services and "url" in keep:
            keep["url"] = f"service:{name}"
        out[name] = keep
    return out
