"""The Plex extension: keeps Maktaba's own channels in Plex Live TV (``plex_livetv``:
tuner + XMLTV DVR, channel map, guide reloads; rules ``plexlive``). Maktaba replaces Plex as
the library: it does not index, organise or read Plex's libraries.

Settings: ``url`` (``MAKTABA_PLEX_URL``) and ``token`` (secret, ``MAKTABA_PLEX_TOKEN``).

Core never imports this package; it adds its stage and trigger edge through
:meth:`PlexExtension.register`.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable, Mapping
from typing import Any

from ... import relay
from ...engine.graph import Edge, GraphBuilder, StageResult
from ...engine.stages.registry import StageImpl
from ...integrations.plex import PlexClient
from ..base import Manifest, SettingField
from .livetv import run_livetv
from .service import ClientFactory, PlexService

__all__ = [
    "PLEX_LIVETV_VERSION",
    "PlexExtension",
    "extension",
    "health_probes",
]

PLEX_LIVETV_VERSION = 2  # 2: Plex's actual channel map counts (drift repair)

MANIFEST = Manifest(
    name="plex",
    version="1",
    description="Register Maktaba's TV tuner and guide in Plex Live TV.",
    settings=(
        SettingField("url", "Plex URL", required=True, env="MAKTABA_PLEX_URL"),
        SettingField("token", "Plex token", secret=True, required=True, env="MAKTABA_PLEX_TOKEN"),
    ),
    stages=("plex_livetv",),
)


def _default_client(settings: Mapping[str, str]) -> PlexClient:
    """With the token (the home): straight to Plex. Without it (a node: the token is in
    home_secrets, which only the home reads): through the home's extension relay."""
    client: PlexClient = relay.plex_client(settings)
    return client


class PlexExtension:
    manifest = MANIFEST

    def __init__(
        self,
        client_factory: ClientFactory = _default_client,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        self.service = PlexService(client_factory, clock)
        self._clock = clock

    # ------------------------------------------------------------------ contract

    def is_configured(self, settings: Mapping[str, str]) -> bool:
        return bool(settings.get("url")) and bool(settings.get("token"))

    def register(self, graph: GraphBuilder) -> None:
        """``plex_livetv`` is tv.maktaba.plex's (Epic 54 story 54.35): its manifest's
        ``[[stage]]``; what leads into it from other stages is added here, as before."""
        from ...engine.graph import _fp

        graph.add_stage(_fp("plex_livetv"))
        # the channel map and the guide after the lineup changes (write_channels)
        if graph.has_stage("write_channels"):
            graph.add_edge("write_channels", Edge("plex_livetv", "same"))

    def impls(self) -> Mapping[str, StageImpl]:
        """In-process, ``plex_livetv`` runs here with this extension's client (direct with
        the token on the home, the relay on a node); in tv.maktaba.plex's runner its entry
        runs the same function through the service door (story 54.35)."""
        return {
            # no input fingerprint: every run looks at Plex (the state is there, not here)
            "plex_livetv": StageImpl(
                run=self._run_livetv, input_fingerprint=None, output_fingerprint=None
            ),
        }

    # ------------------------------------------------------------------ stage

    async def _run_livetv(self, db: Any, job: Any, ctx: Any) -> StageResult:
        return await run_livetv(db, job, ctx, self.service)

    # ------------------------------------------------------------------ health

    def health_probes(
        self, settings: Mapping[str, str]
    ) -> dict[str, Callable[[], Awaitable[float]]]:
        """``net.plex_ms``: round-trip to ``/identity`` in ms, -1 when unreachable."""
        if not self.is_configured(settings):
            return {}
        factory = self.service.client

        async def probe() -> float:
            t0 = time.perf_counter()
            try:
                await asyncio.to_thread(lambda: factory(settings).ping())
            except Exception:  # noqa: BLE001 - any failure means unreachable
                return -1.0
            return (time.perf_counter() - t0) * 1000.0

        return {"net.plex_ms": probe}


extension = PlexExtension()


def health_probes(settings: Mapping[str, str]) -> dict[str, Callable[[], Awaitable[float]]]:
    return extension.health_probes(settings)
