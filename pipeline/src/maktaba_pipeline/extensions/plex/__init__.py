"""The Plex extension: indexes each library's Plex items, writes each detected
season to Plex as a manual collection, and keeps Maktaba's channels in Plex Live TV
(``plex_livetv``: tuner + XMLTV DVR, channel map, guide reloads; rules ``plexlive``).

Settings: ``url`` (``MAKTABA_PLEX_URL``), ``token`` (secret, ``MAKTABA_PLEX_TOKEN``)
and ``path_map`` (``MAKTABA_PATH_MAP``: how *Plex* sees the NAS paths the database
stores, same syntax as :class:`~maktaba_pipeline.engine.paths.PathMap`). With the
canonical paths of story 31.3 the map is per share, e.g.
``smb://nas/Originals/=/volume1/Originals/`` (Plex on the Synology sees
``/volume1/<share>``); legacy prefixes (``/Volumes/Originals/=...``) keep working for
paths stored before the migration.

Core never imports this package; it adds its stage and trigger edge through
:meth:`PlexExtension.register`.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable, Mapping
from typing import Any

from ... import relay
from ...engine.graph import Edge, GraphBuilder, StageResult, Subject
from ...engine.stages.registry import StageImpl
from ...integrations.plex import PlexClient
from ..base import Manifest, SettingField
from .collection import (
    ClientFactory,
    PlexService,
    fingerprint,
    run_collection,
)
from .collections_import import run_collections_import
from .groups import fingerprint as groups_fingerprint
from .groups import run_groups
from .index import run_index
from .livetv import run_livetv

__all__ = [
    "PLEX_EXPORT_VERSION",
    "PLEX_INDEX_VERSION",
    "PlexExtension",
    "extension",
    "health_probes",
]

PLEX_EXPORT_VERSION = 1
PLEX_INDEX_VERSION = 1
PLEX_GROUPS_VERSION = 1
PLEX_LIVETV_VERSION = 2  # 2: Plex's actual channel map counts (drift repair)
PLEX_COLLECTIONS_IMPORT_VERSION = 1

MANIFEST = Manifest(
    name="plex",
    version="1",
    description=(
        "Index each library's Plex items once, then write each detected season to Plex"
        " as an ordered manual collection."
    ),
    settings=(
        SettingField("url", "Plex URL", required=True, env="MAKTABA_PLEX_URL"),
        SettingField("token", "Plex token", secret=True, required=True, env="MAKTABA_PLEX_TOKEN"),
        SettingField(
            "path_map",
            "Path map (Plex's view of the NAS paths: smb://nas/Share/=/volume1/Share/)",
            env="MAKTABA_PATH_MAP",
        ),
    ),
    stages=(
        "plex_index",
        "plex_collection",
        "plex_groups",
        "plex_livetv",
        "plex_collections_import",
    ),
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
        """The Plex stages are tv.maktaba.plex's (Epic 54 story 54.35): each is its
        manifest's ``[[stage]]`` (the stage's own edges included); what leads into them from
        other stages is added here, as before."""
        from ...engine.graph import _fp

        for stage in (
            _fp("plex_index"),
            _fp("plex_collections_import"),
            _fp("plex_collection"),
            _fp("plex_groups"),
            _fp("plex_livetv"),
        ):
            graph.add_stage(stage)
        if graph.has_stage("scan"):  # a rescan may add, move or drop files Plex lists
            graph.add_edge("scan", Edge("plex_index", "same"))
        graph.add_edge("detect", Edge("plex_collection", "seasons_changed"))
        for src in ("topic_collections", "clip_groups"):
            if graph.has_stage(src):
                graph.add_edge(src, Edge("plex_groups", "same"))
        # the channel map and the guide after the lineup changes (write_channels)
        if graph.has_stage("write_channels"):
            graph.add_edge("write_channels", Edge("plex_livetv", "same"))

    def impls(self) -> Mapping[str, StageImpl]:
        """In-process, each Plex stage runs here with this extension's client (direct with
        the token on the home, the relay on a node); in tv.maktaba.plex's runner its entry
        runs the same functions through the service door (story 54.35)."""
        return {
            "plex_index": StageImpl(
                run=self._run_index, input_fingerprint=None, output_fingerprint=None
            ),
            "plex_collection": StageImpl(
                run=self._run, input_fingerprint=self._input_fingerprint, output_fingerprint=None
            ),
            "plex_groups": StageImpl(
                run=self._run_groups,
                input_fingerprint=self._groups_fingerprint,
                output_fingerprint=None,
            ),
            # no input fingerprint: every run looks at Plex (the state is there, not here)
            "plex_collections_import": StageImpl(
                run=self._run_collections_import, input_fingerprint=None, output_fingerprint=None
            ),
            "plex_livetv": StageImpl(
                run=self._run_livetv, input_fingerprint=None, output_fingerprint=None
            ),
        }

    # ------------------------------------------------------------------ stage

    async def _run(self, db: Any, job: Any, ctx: Any) -> StageResult:
        return await run_collection(db, job, ctx, self.service)

    async def _run_index(self, db: Any, job: Any, ctx: Any) -> StageResult:
        return await run_index(db, job, ctx, self.service)

    async def _run_groups(self, db: Any, job: Any, ctx: Any) -> StageResult:
        return await run_groups(db, job, ctx, self.service)

    async def _run_collections_import(self, db: Any, job: Any, ctx: Any) -> StageResult:
        return await run_collections_import(db, job, ctx, self.service)

    async def _run_livetv(self, db: Any, job: Any, ctx: Any) -> StageResult:
        return await run_livetv(db, job, ctx, self.service)

    async def _groups_fingerprint(self, db: Any, subject: Subject) -> str | None:
        row = await db.fetchrow("SELECT write_mode FROM job_stages WHERE stage = $1", "plex_groups")
        return await groups_fingerprint(
            db, subject, "auto" if row is None else str(row["write_mode"])
        )

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
