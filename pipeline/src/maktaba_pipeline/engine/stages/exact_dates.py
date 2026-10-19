"""``exact_dates``: exact upload dates for series members (mediaintel ``exact`` step).

Ported from ``scan_youtube.exact_dates`` / ``scan --exact``. ``youtube_sync`` lists a channel
with month-level dates (``date_quality = 'approximate'``); an episode's air date decides its
order in a season, so for the videos that matter this stage asks YouTube for the page of each
one and stores the exact day (``date_quality = 'exact'``) and the description. Detect runs
again on the result.

Subject: a library. Pool ``network_yt``. **Off until the ``exactdates`` rules say
``enabled``**; polite by design: one request at a time, a randomised delay between two
(``delay_s``), ``max_per_run`` to bound a run. YouTube's bot check stops the run
(non-retryable ``youtube_bot_check``); what was fetched stays stored and the next run
carries on with the rest.

Which videos: those linked to a YouTube video whose date is not exact and that are members of
a series or of a playlist (``scope = series``), or all of them (``scope = all``); series
members first.
"""

from __future__ import annotations

import asyncio
import random
from collections.abc import Awaitable, Callable
from datetime import date
from typing import Any
from uuid import UUID

from ...integrations.youtube import BotCheck, VideoInfoFetcher, exact_info
from ...schedule.tv_settings import ExactDatesConfig
from ..context import StageContext, StageFailure
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..sql import is_pg, uid
from . import youtube_sync
from .registry import StageImpl

__all__ = ["IMPL", "candidates", "set_sleep"]

_sleep: Callable[[float], Awaitable[None]] = asyncio.sleep


def set_sleep(fn: Callable[[float], Awaitable[None]] | None) -> Callable[[float], Awaitable[None]]:
    """Replace the delay between two requests (tests); None restores ``asyncio.sleep``."""
    global _sleep
    previous = _sleep
    _sleep = fn or asyncio.sleep
    return previous


_CANDIDATES = """
SELECT yv.youtube_id AS yid,
       MAX(CASE WHEN se.video_id IS NOT NULL THEN 1 ELSE 0 END) AS in_series,
       MAX(CASE WHEN pi.youtube_id IS NOT NULL THEN 1 ELSE 0 END) AS in_playlist
  FROM youtube_videos yv
  JOIN media_metadata_enrichment m
    ON m.external_id = yv.youtube_id AND m.provider = 'youtube' AND m.is_dismissed = {false}
  JOIN videos v ON v.id = m.video_id AND v.library_id = $1 AND v.deleted_at IS NULL
  LEFT JOIN series_episodes se ON se.video_id = v.id
  LEFT JOIN youtube_playlist_items pi ON pi.youtube_id = yv.youtube_id
 WHERE yv.date_quality IS NULL OR yv.date_quality <> 'exact'
 GROUP BY yv.youtube_id
{having}
 ORDER BY in_series DESC, yv.youtube_id
"""


async def candidates(db: Any, library_id: UUID, cfg: ExactDatesConfig) -> list[str]:
    """YouTube ids of the library's linked videos that still lack an exact date, series
    members first, at most ``max_per_run`` of them (0: all)."""
    having = (
        ""
        if cfg.scope == "all"
        else "HAVING MAX(CASE WHEN se.video_id IS NOT NULL THEN 1 ELSE 0 END) = 1"
        " OR MAX(CASE WHEN pi.youtube_id IS NOT NULL THEN 1 ELSE 0 END) = 1"
    )
    sql = _CANDIDATES.replace("{false}", "false" if is_pg(db) else "0").replace("{having}", having)
    ids = [str(r["yid"]) for r in await db.fetch(sql, uid(library_id))]
    return ids[: cfg.max_per_run] if cfg.max_per_run > 0 else ids


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """The switch, the scope and the videos still waiting: nothing new, nothing to ask."""
    cfg: ExactDatesConfig = (await _cfg(db))[0]
    ids = await candidates(db, subject.id, cfg) if cfg.enabled else []
    return canonical_hash({"enabled": cfg.enabled, "scope": cfg.scope, "ids": ids})


async def _cfg(db: Any) -> tuple[ExactDatesConfig, dict[str, Any]]:
    from ... import rules

    cfg, changes, _ = await rules.read(db, "exactdates")
    return cfg, changes


def _stored(db: Any, d: date) -> date | str:
    return d if is_pg(db) else d.isoformat()


async def run(db: Any, job: Any, ctx: StageContext) -> StageResult:
    cfg: ExactDatesConfig = await ctx.rules("exactdates")
    if not cfg.enabled:
        return StageResult(
            output_hash=canonical_hash("exact_dates:off"),
            outcome="unchanged",
            summary={"enabled": False},
        )
    library_id: UUID = job.subject.id
    ids = await candidates(db, library_id, cfg)
    if not ids:
        return StageResult(
            output_hash=canonical_hash("exact_dates:none"),
            outcome="unchanged",
            summary={"enabled": True, "wanted": 0, "fetched": 0},
        )
    try:
        fetcher: VideoInfoFetcher = youtube_sync.new_fetcher()  # type: ignore[assignment]
    except ImportError as exc:
        raise StageFailure(
            "yt_dlp_missing",
            "yt-dlp is not installed (pip install 'maktaba-pipeline[mediaintel]')",
            retryable=False,
        ) from exc
    cookies = cfg.cookies_from_browser or None
    done: list[tuple[str, str | None]] = []  # (youtube id, exact date)
    unread = 0
    for n, yid in enumerate(ids, 1):
        await ctx.check_cancel()
        await ctx.progress("exact dates", n - 1, len(ids), yid)
        await _sleep(cfg.delay_s * (0.5 + random.random()))  # noqa: S311 - politeness jitter
        try:
            info = await asyncio.to_thread(
                fetcher.video_info, yid, lang=cfg.lang, cookies_from_browser=cookies
            )
        except BotCheck as exc:
            raise StageFailure(
                "youtube_bot_check",
                f"Sign in to confirm you're not a bot (YouTube) after {len(done)} videos; "
                "what was fetched is kept, the next run carries on. A signed-in browser "
                "session (rule cookies_from_browser) lifts most limits.",
                retryable=False,
            ) from exc
        if not info:
            unread += 1  # private or removed: nothing to learn
            continue
        got = exact_info(info)
        await _store(db, yid, got.upload_date, got.description)
        done.append((yid, got.upload_date.isoformat() if got.upload_date else None))
    return StageResult(
        output_hash=canonical_hash({"dates": sorted(done, key=lambda x: x[0])}),
        summary={
            "enabled": True,
            "wanted": len(ids),
            "fetched": len(done),
            "exact": sum(1 for _, d in done if d),
            "unreadable": unread,
        },
    )


async def _store(db: Any, youtube_id: str, d: date | None, description: str | None) -> None:
    if d is not None:
        await db.execute(
            "UPDATE youtube_videos SET upload_date = $2, date_quality = 'exact',"
            " description = COALESCE(description, $3) WHERE youtube_id = $1",
            youtube_id,
            _stored(db, d),
            description,
        )
    elif description is not None:
        await db.execute(
            "UPDATE youtube_videos SET description = COALESCE(description, $2)"
            " WHERE youtube_id = $1",
            youtube_id,
            description,
        )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
