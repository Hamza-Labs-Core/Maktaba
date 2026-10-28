"""Binding of stage names to implementations (handler plus fingerprints).

Core stages are imported lazily on first lookup so that importing the graph
never pulls in ffmpeg wrappers or HTTP clients. Extensions register their
implementations through :func:`register_impl` (see ``engine.loader``).
"""

from __future__ import annotations

import importlib
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any

from ..graph import InputFingerprint, OutputFingerprint, StageHandler

if TYPE_CHECKING:
    from ...db.jobs import Job

__all__ = ["CORE_MODULES", "StageImpl", "get_impl", "register_impl", "reset_registry"]


@dataclass(frozen=True, slots=True)
class StageImpl:
    run: StageHandler | None = None
    input_fingerprint: InputFingerprint | None = None
    output_fingerprint: OutputFingerprint | None = None
    #: why this worker cannot run the stage (a platform or an optional extra it
    #: lacks), or None; a worker never claims a stage it cannot run
    unavailable: Callable[[], str | None] | None = None
    #: ``(job, earlier input hash, input fingerprint)``: whether the subject's last
    #: success stands for this job on this worker (the run is skipped). None: when the
    #: two are equal. May raise ``JobDeferred`` to give the job back for another worker.
    still_current: Callable[[Job, str, str], bool] | None = None
    #: ``(db, serving)``: what the worker re-reads before each stage selection (the
    #: rules :attr:`unavailable` reads), and the upkeep a worker that serves the stage
    #: does then; see :func:`maktaba_pipeline.cluster.stages.refresh_stages`
    refresh: Callable[[Any, bool], Awaitable[None]] | None = None


#: stage name -> module exposing ``IMPL: StageImpl``. ``legacy`` is special: it
#: exposes ``IMPLS`` (output fingerprints for the stages that predate the engine).
CORE_MODULES: dict[str, str] = {
    "content_hash": "maktaba_pipeline.engine.stages.content_hash",
    "transcribe": "maktaba_pipeline.engine.stages.transcribe",
    "subtitle_gen": "maktaba_pipeline.engine.stages.subtitle_gen",
    "index": "maktaba_pipeline.engine.stages.index",
    "chapter_infer": "maktaba_pipeline.engine.stages.chapter_infer",
    "chapter_thumbs": "maktaba_pipeline.engine.stages.chapter_thumbs",
    "topic_assign": "maktaba_pipeline.engine.stages.topic_assign",
    "topic_recluster": "maktaba_pipeline.engine.stages.topic_recluster",
    "thumb_hash": "maktaba_pipeline.engine.stages.thumb_hash",
    "thumb_small": "maktaba_pipeline.engine.stages.thumb_small",
    "review_pages": "maktaba_pipeline.engine.stages.review_pages",
    "topic_collections": "maktaba_pipeline.engine.stages.topic_collections",
    "promo_links": "maktaba_pipeline.engine.stages.promo_links",
    "clip_groups": "maktaba_pipeline.engine.stages.clip_groups",
    "link": "maktaba_pipeline.engine.stages.link",
    "fingerprint": "maktaba_pipeline.engine.stages.fingerprint",
    "style_sample": "maktaba_pipeline.engine.stages.style_sample",
    "youtube_sync": "maktaba_pipeline.engine.stages.youtube_sync",
    "detect": "maktaba_pipeline.engine.stages.detect",
    "fp_gate": "maktaba_pipeline.engine.stages.fp_gate",
    "tag": "maktaba_pipeline.engine.stages.tag",
    "check_tags": "maktaba_pipeline.engine.stages.check_tags",
    "check_clips": "maktaba_pipeline.engine.stages.check_clips",
    "books": "maktaba_pipeline.engine.stages.books",
    "display_title": "maktaba_pipeline.engine.stages.display_title",
    "rate_style": "maktaba_pipeline.engine.stages.rate_style",
    "plan_channels": "maktaba_pipeline.engine.stages.plan_channels",
    "write_channels": "maktaba_pipeline.engine.stages.write_channels",
    "tv_stage": "maktaba_pipeline.engine.stages.tv_stage",
    "tv_swap": "maktaba_pipeline.engine.stages.tv_swap",
    "tv_gc": "maktaba_pipeline.engine.stages.tv_gc",
    "tv_rollback": "maktaba_pipeline.engine.stages.tv_rollback",
    "exact_dates": "maktaba_pipeline.engine.stages.exact_dates",
    "repair_broken": "maktaba_pipeline.engine.stages.repair_broken",
    "access_check": "maktaba_pipeline.engine.stages.access_check",
    "storage_canonicalize": "maktaba_pipeline.engine.stages.storage_canonicalize",
    "enrich": "maktaba_pipeline.engine.stages.enrich",
    # Epic 47 (offline, MP4, no duplicates); all but trash_purge are stubs until their
    # stories ship (the graph keeps them off: StageDef.unreleased)
    "convert_mp4": "maktaba_pipeline.engine.stages.convert_mp4",
    "dedup_scan": "maktaba_pipeline.engine.stages.dedup_scan",
    "dedup_apply": "maktaba_pipeline.engine.stages.dedup_apply",
    "youtube_fetch": "maktaba_pipeline.engine.stages.youtube_fetch",
    "youtube_backfill": "maktaba_pipeline.engine.stages.youtube_backfill",
    "trash_purge": "maktaba_pipeline.engine.stages.trash_purge",
    "file_restore": "maktaba_pipeline.engine.stages.file_restore",
}
_LEGACY_MODULE = "maktaba_pipeline.engine.stages.legacy"

_IMPLS: dict[str, StageImpl] = {}
_LEGACY_LOADED = False


def register_impl(name: str, impl: StageImpl) -> None:
    _IMPLS[name] = impl


def reset_registry() -> None:
    """Drop registered (extension) implementations; for tests."""
    global _LEGACY_LOADED
    _IMPLS.clear()
    _LEGACY_LOADED = False


def get_impl(name: str) -> StageImpl | None:
    global _LEGACY_LOADED
    if name in _IMPLS:
        return _IMPLS[name]
    if name in CORE_MODULES:
        impl = importlib.import_module(CORE_MODULES[name]).IMPL
        _IMPLS[name] = impl
        return impl  # type: ignore[no-any-return]
    if not _LEGACY_LOADED:
        # a stage that has moved onto the engine (a core module) keeps that module's
        # implementation, whichever stage happened to load the legacy table first
        legacy = importlib.import_module(_LEGACY_MODULE).IMPLS
        _IMPLS.update({k: v for k, v in legacy.items() if k not in CORE_MODULES})
        _LEGACY_LOADED = True
    return _IMPLS.get(name)
