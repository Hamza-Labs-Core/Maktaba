"""The dizqueTV extension: custom shows per season and the planned channels.

Settings: ``url`` (``MAKTABA_DIZQUETV_URL``). Building programmes also needs the
Plex settings (the ``plex`` extension's ``url`` and ``token``): dizqueTV plays
from Plex, so every programme is built from Plex metadata.

Core never imports this package; it adds its stages and trigger edges through
:meth:`DizqueTVExtension.register`.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable, Mapping
from typing import Any

from ...engine.graph import Edge, GraphBuilder, StageDef, StageResult, Subject
from ...engine.stages.registry import StageImpl
from ...integrations.dizquetv import DizqueTV, tz_offset_minutes
from ...integrations.plex import PlexClient
from ..base import Manifest, SettingField
from .audit import AuditService, run_audit
from .audit import fingerprint as audit_fingerprint
from .channels import ChannelsService, run_channels
from .channels import fingerprint as channels_fingerprint
from .exporter import EXPORT_NAME, export_channel
from .shows import ShowsService, run_show
from .shows import fingerprint as show_fingerprint

__all__ = [
    "DZ_AUDIT_VERSION",
    "DZ_CHANNELS_VERSION",
    "DZ_SHOW_VERSION",
    "DizqueTVExtension",
    "extension",
    "health_probes",
]

DZ_SHOW_VERSION = 1
DZ_CHANNELS_VERSION = 1
DZ_AUDIT_VERSION = 1

MANIFEST = Manifest(
    name="dizquetv",
    version="1",
    description="Keep dizqueTV custom shows and channels in step with the library and the plan.",
    settings=(SettingField("url", "dizqueTV URL", required=True, env="MAKTABA_DIZQUETV_URL"),),
    stages=("dizquetv_show", "dizquetv_channels", "dizquetv_audit"),
)


def _default_dz(settings: Mapping[str, str]) -> DizqueTV:
    return DizqueTV(settings.get("url", ""))


def _default_plex(settings: Mapping[str, str]) -> PlexClient:
    return PlexClient(settings.get("url", ""), settings.get("token", ""))


class DizqueTVExtension:
    manifest = MANIFEST

    def __init__(
        self,
        dz_factory: Callable[[Mapping[str, str]], DizqueTV] = _default_dz,
        plex_factory: Callable[[Mapping[str, str]], PlexClient] = _default_plex,
        tz_offset: Callable[[], int] = tz_offset_minutes,
    ) -> None:
        self.shows = ShowsService(dz_factory, plex_factory)
        self.channels = ChannelsService(dz_factory, plex_factory, tz_offset)
        self.audit = AuditService(dz_factory)
        self._dz_factory = dz_factory

    # ------------------------------------------------------------------ contract

    def is_configured(self, settings: Mapping[str, str]) -> bool:
        return bool(settings.get("url"))

    def register(self, graph: GraphBuilder) -> None:
        graph.add_stage(
            StageDef(
                name="dizquetv_show",
                label="dizqueTV show",
                subject_kinds=("season",),
                pool="network_tv",
                lane="seasons",
                lane_order=5,
                version=DZ_SHOW_VERSION,
                external_write=True,
                entry_for=("season",),
            )
        )
        graph.add_stage(
            StageDef(
                name="dizquetv_channels",
                label="dizqueTV channels",
                subject_kinds=("catalog",),
                pool="network_tv",
                lane="channels",
                lane_order=3,
                version=DZ_CHANNELS_VERSION,
                external_write=True,
                entry_for=("catalog",),
                # uploads the logo / watermark files from the home's folders
                placement="home",
            )
        )
        graph.add_stage(
            StageDef(
                name="dizquetv_audit",
                label="Channel audit",
                subject_kinds=("catalog",),
                pool="audits",
                lane="channels",
                lane_order=4,
                version=DZ_AUDIT_VERSION,
                entry_for=("catalog",),
                debounce_sec=60,
                debounce_max_sec=900,
                # writes its report into the home's reviews folder
                placement="home",
            )
        )
        graph.add_edge("dizquetv_channels", Edge("dizquetv_audit", "same"))
        graph.add_edge("detect", Edge("dizquetv_show", "seasons_changed"))
        if graph.has_stage("plex_collection"):
            graph.add_edge("plex_collection", Edge("dizquetv_show", "same"))
        if graph.has_stage("plex_index"):  # programmes are built from the saved Plex records
            graph.add_edge("plex_index", Edge("dizquetv_show", "seasons_changed"))
        graph.add_edge("plan_channels", Edge("dizquetv_channels", "same"))

    def impls(self) -> Mapping[str, StageImpl]:
        return {
            "dizquetv_show": StageImpl(
                run=self._run_show,
                input_fingerprint=self._show_fingerprint,
                output_fingerprint=None,
            ),
            "dizquetv_channels": StageImpl(
                run=self._run_channels,
                input_fingerprint=self._channels_fingerprint,
                output_fingerprint=None,
            ),
            "dizquetv_audit": StageImpl(
                run=self._run_audit,
                input_fingerprint=audit_fingerprint,
                output_fingerprint=None,
            ),
        }

    def plan_exporters(self) -> dict[str, Callable[[Any, Any], dict[str, Any]]]:
        """Contribute the ``dizquetv`` block to each channel of the plan (core hook)."""
        return {EXPORT_NAME: export_channel}

    # ------------------------------------------------------------------ stages

    async def _run_show(self, db: Any, job: Any, ctx: Any) -> StageResult:
        return await run_show(db, job, ctx, self.shows)

    async def _run_channels(self, db: Any, job: Any, ctx: Any) -> StageResult:
        return await run_channels(db, job, ctx, self.channels)

    async def _run_audit(self, db: Any, job: Any, ctx: Any) -> StageResult:
        return await run_audit(db, job, ctx, self.audit)

    @staticmethod
    async def _write_mode(db: Any, stage: str) -> str:
        row = await db.fetchrow("SELECT write_mode FROM job_stages WHERE stage = $1", stage)
        return "auto" if row is None else str(row["write_mode"])

    async def _show_fingerprint(self, db: Any, subject: Subject) -> str | None:
        return await show_fingerprint(db, subject, await self._write_mode(db, "dizquetv_show"))

    async def _channels_fingerprint(self, db: Any, subject: Subject) -> str | None:
        return await channels_fingerprint(
            db, subject, await self._write_mode(db, "dizquetv_channels")
        )

    # ------------------------------------------------------------------ health

    def health_probes(
        self, settings: Mapping[str, str]
    ) -> dict[str, Callable[[], Awaitable[float]]]:
        """``net.tv_ms``: round-trip to ``/api/channels`` in ms, -1 when unreachable."""
        if not self.is_configured(settings):
            return {}
        factory = self._dz_factory

        async def probe() -> float:
            t0 = time.perf_counter()
            try:
                await asyncio.to_thread(lambda: factory(settings).ping())
            except Exception:  # noqa: BLE001 - any failure means unreachable
                return -1.0
            return (time.perf_counter() - t0) * 1000.0

        return {"net.tv_ms": probe}


extension = DizqueTVExtension()


def health_probes(settings: Mapping[str, str]) -> dict[str, Callable[[], Awaitable[float]]]:
    return extension.health_probes(settings)
