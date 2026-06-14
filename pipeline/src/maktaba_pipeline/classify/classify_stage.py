"""The ``classify`` stage body (Story 26.7 §2).

``classify`` is the local-only, network-free stage inserted between
``index`` and ``thumbnail``. It runs the deterministic title parser
(Story 26.1) — and, as the topic/entity extractor (26.2) lands, that too
— persists the result to ``media_parsed_titles`` (slot 0073), then:

1. fire-and-forgets an ``enrich`` job *iff* enrichment is enabled and a
   provider key is configured (ordering guarantee: enrich never precedes
   its classify); and
2. schedules the debounced library group passes (series + collections).

Critically, classification is an **enhancement, not a gate**: a failure
in the parse/persist body is logged and recorded but **never re-raised**,
so the orchestrator still advances the video to ``thumbnail`` → ``READY``
(Story 26.7 D3, mirroring chapter-inference isolation).
"""

from __future__ import annotations

from collections.abc import Sequence
from datetime import UTC, datetime
from uuid import UUID

from ..enrich import EnrichSettings, ProviderKey, should_enqueue_enrich
from ..enrich.jobs import Conn, enqueue_enrich
from ..log import get_logger
from . import title_parser
from .group_scheduler import GroupScheduler

__all__ = ["ClassifyResult", "run_classify", "write_parsed_title"]

log = get_logger(component="classify_stage")


class ClassifyResult:
    """Outcome of a classify run, for the orchestrator's metrics."""

    __slots__ = ("classified", "enrich_enqueued", "error")

    def __init__(self, *, classified: bool, enrich_enqueued: bool, error: str | None) -> None:
        self.classified = classified
        self.enrich_enqueued = enrich_enqueued
        self.error = error


_UPSERT_PARSED_TITLE_SQL = """
INSERT INTO media_parsed_titles
    (video_id, show_name, season, episode, absolute_number, year,
     resolution, codec, release_group, edition, confidence, parser_version,
     created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
ON CONFLICT (video_id) DO UPDATE SET
    show_name = EXCLUDED.show_name, season = EXCLUDED.season,
    episode = EXCLUDED.episode, absolute_number = EXCLUDED.absolute_number,
    year = EXCLUDED.year, resolution = EXCLUDED.resolution,
    codec = EXCLUDED.codec, release_group = EXCLUDED.release_group,
    edition = EXCLUDED.edition, confidence = EXCLUDED.confidence,
    parser_version = EXCLUDED.parser_version, updated_at = EXCLUDED.updated_at
"""


async def write_parsed_title(
    conn: Conn,
    video_id: UUID,
    parsed: title_parser.ParsedTitle,
    *,
    now: datetime | None = None,
) -> None:
    """Persist (idempotently upsert) the parsed title for a video."""
    ts = now or datetime.now(UTC)
    await conn.fetchrow(
        _UPSERT_PARSED_TITLE_SQL,
        str(video_id),
        parsed.show_name,
        parsed.season,
        parsed.episode,
        None,  # absolute_number — not recovered by the v1 parser
        parsed.year,
        parsed.resolution,
        parsed.video_codec,
        parsed.release_group,
        parsed.edition,
        parsed.confidence,
        str(parsed.parser_version),
        ts,
    )


async def run_classify(
    conn: Conn,
    *,
    video_id: UUID,
    library_id: UUID,
    filename: str,
    dirnames: Sequence[str] = (),
    settings: EnrichSettings,
    providers: Sequence[ProviderKey],
    scheduler: GroupScheduler | None = None,
) -> ClassifyResult:
    """Run the classify stage body for one video.

    Returns a :class:`ClassifyResult` for the orchestrator's metrics. A
    failure in the parse/persist body is captured in ``error`` and never
    raised — the caller (the stage handler) advances the video regardless
    (Story 26.7 D3).
    """
    error: str | None = None
    classified = False
    try:
        parsed = title_parser.parse(filename, dirnames=tuple(dirnames))
        await write_parsed_title(conn, video_id, parsed)
        classified = True
    except Exception as exc:  # noqa: BLE001 — classification must not gate READY (D3)
        error = str(exc)
        log.warning("classify_failed", video_id=str(video_id), error=error)

    # Ordering guarantee: enrich is enqueued only after classify ran, and
    # only when enabled + a provider key exists (D4).
    enrich_enqueued = False
    if should_enqueue_enrich(settings, providers):
        try:
            enqueued = await enqueue_enrich(conn, video_id)
            enrich_enqueued = enqueued is not None
        except Exception as exc:  # noqa: BLE001 — enrich enqueue is best-effort
            log.warning("enrich_enqueue_failed", video_id=str(video_id), error=str(exc))

    # Library-level grouping is debounced + coalesced (D5).
    if scheduler is not None:
        await scheduler.schedule(library_id)

    return ClassifyResult(classified=classified, enrich_enqueued=enrich_enqueued, error=error)
