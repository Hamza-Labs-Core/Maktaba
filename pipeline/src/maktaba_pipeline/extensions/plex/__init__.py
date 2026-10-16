"""The Plex extension: writes each detected season to Plex as a manual collection.

Settings: ``url`` (``MAKTABA_PLEX_URL``), ``token`` (secret, ``MAKTABA_PLEX_TOKEN``)
and ``path_map`` (``MAKTABA_PATH_MAP``: how *Plex* sees the NAS paths the database
stores, same syntax as :class:`~maktaba_pipeline.engine.paths.PathMap`).

Core never imports this package; it adds its stage and trigger edge through
:meth:`PlexExtension.register`.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable, Mapping
from typing import Any

from ...engine.graph import Edge, GraphBuilder, StageDef, StageResult, Subject
from ...engine.stages.registry import StageImpl
from ...integrations.plex import PlexClient
from ..base import Manifest, SettingField
from .collection import (
    ClientFactory,
    PlexService,
    fingerprint,
    run_collection,
)

__all__ = ["PLEX_EXPORT_VERSION", "PlexExtension", "extension", "health_probes"]

PLEX_EXPORT_VERSION = 1

MANIFEST = Manifest(
    name="plex",
    version="1",
    description="Write each detected season to Plex as an ordered manual collection.",
    settings=(
        SettingField("url", "Plex URL", required=True, env="MAKTABA_PLEX_URL"),
        SettingField("token", "Plex token", secret=True, required=True, env="MAKTABA_PLEX_TOKEN"),
        SettingField(
            "path_map",
            "Path map (Plex's view of the NAS paths)",
            env="MAKTABA_PATH_MAP",
        ),
    ),
    stages=("plex_collection",),
)


def _default_client(settings: Mapping[str, str]) -> PlexClient:
    return PlexClient(settings.get("url", ""), settings.get("token", ""))


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
        graph.add_stage(
            StageDef(
                name="plex_collection",
                label="Plex collection",
                subject_kinds=("season",),
                pool="network_plex",
                lane="seasons",
                lane_order=4,
                version=PLEX_EXPORT_VERSION,
                external_write=True,
                entry_for=("season",),
            )
        )
        graph.add_edge("detect", Edge("plex_collection", "seasons_changed"))

    def impls(self) -> Mapping[str, StageImpl]:
        return {
            "plex_collection": StageImpl(
                run=self._run, input_fingerprint=self._input_fingerprint, output_fingerprint=None
            )
        }

    # ------------------------------------------------------------------ stage

    async def _run(self, db: Any, job: Any, ctx: Any) -> StageResult:
        return await run_collection(db, job, ctx, self.service)

    async def _input_fingerprint(self, db: Any, subject: Subject) -> str | None:
        row = await db.fetchrow(
            "SELECT write_mode FROM job_stages WHERE stage = $1", "plex_collection"
        )
        return await fingerprint(db, subject, "auto" if row is None else str(row["write_mode"]))

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
