"""``link``: match a local video to its YouTube video (plan 06-11 section 3.2).

Order of evidence, first hit wins (``mapped.link_method`` records which, the
enrichment ``confidence`` follows plan section 4.4):

1. ``info_json`` (1.0) - the video's own yt-dlp ``<stem>.info.json`` sidecar;
2. ``exact_title`` (1.0) - the file stem equals yt-dlp's sanitised filename for the title;
3. ``folded_title`` (0.9) - equal after Arabic folding, unique among free candidates;
4. ``title_duration`` (0.8) - folded titles equal but ambiguous; the duration (+-3 s) picks;
5. ``fuzzy_title_duration`` (0.6) - the title was edited after download: duration within
   1.5 s and at least half the words shared, with exactly one candidate.

A YouTube title edited after download that matches none of these stays unlinked and is still
grouped from local evidence. The result goes into ``media_metadata_enrichment``
(``provider='youtube'``); the row is auto-accepted only when the user never edited that
video's metadata (slot 0078: never overwrite a user edit).
"""

from __future__ import annotations

import os
import uuid
from collections import defaultdict
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from uuid import UUID

from ...db.jobs import Job
from ...discovery.ytdlp_sidecars import VideoInfo, is_real_playlist, parse_info_json
from ...text.arabic import fold, ytdlp_filename
from ..context import StageContext
from ..graph import LINK_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, now, to_date, to_ts, uid
from ._common import load_video, require_video
from .registry import StageImpl

__all__ = ["IMPL", "Candidate", "LinkMatch", "match_candidates"]

CONFIDENCE = {
    "info_json": 1.0,
    "exact_title": 1.0,
    "folded_title": 0.9,
    "title_duration": 0.8,
    "fuzzy_title_duration": 0.6,
}


@dataclass(frozen=True, slots=True)
class Candidate:
    youtube_id: str
    title: str
    duration_s: float | None


@dataclass(frozen=True, slots=True)
class LinkMatch:
    youtube_id: str
    method: str

    @property
    def confidence(self) -> float:
        return CONFIDENCE[self.method]


def _jaccard(a: set[str], b: set[str]) -> float:
    return len(a & b) / len(a | b) if a and b else 0.0


def match_candidates(
    stem: str,
    duration_s: float | None,
    candidates: list[Candidate],
    taken: set[str],
    *,
    index: tuple[dict[str, list[Candidate]], dict[str, list[Candidate]]] | None = None,
) -> LinkMatch | None:
    """Match one file stem against the channel's YouTube videos (pure; port of ``link.link``)."""
    if index is None:
        index = build_index(candidates)
    by_exact, by_fold = index
    free = [c for c in by_exact.get(stem, []) if c.youtube_id not in taken]
    if len(free) == 1:
        return LinkMatch(free[0].youtube_id, "exact_title")
    folded = [c for c in by_fold.get(fold(stem), []) if c.youtube_id not in taken]
    if len(folded) == 1:
        return LinkMatch(folded[0].youtube_id, "folded_title")
    if len(folded) > 1 and duration_s:
        near = [c for c in folded if c.duration_s and abs(c.duration_s - duration_s) <= 3]
        return LinkMatch(near[0].youtube_id, "title_duration") if len(near) == 1 else None
    if not folded and duration_s:
        mine = set(fold(stem).split())
        fuzzy = [
            c
            for c in candidates
            if c.youtube_id not in taken
            and c.duration_s
            and abs(c.duration_s - duration_s) <= 1.5
            and _jaccard(mine, set(fold(c.title).split())) >= 0.5
        ]
        if len(fuzzy) == 1:
            return LinkMatch(fuzzy[0].youtube_id, "fuzzy_title_duration")
    return None


def build_index(
    candidates: list[Candidate],
) -> tuple[dict[str, list[Candidate]], dict[str, list[Candidate]]]:
    by_exact: dict[str, list[Candidate]] = defaultdict(list)
    by_fold: dict[str, list[Candidate]] = defaultdict(list)
    for c in candidates:
        by_exact[ytdlp_filename(c.title)].append(c)
        by_fold[fold(c.title)].append(c)
    return by_exact, by_fold


# A job links one video, but the channel's candidate index is the same for every video of the
# library; keep the last few so a 10k-video link wave does not rebuild it 10k times.
_INDEX_CACHE: dict[tuple[str, str], tuple[list[Candidate], Any]] = {}


async def _candidates(db: Any, library_id: UUID) -> tuple[list[Candidate], Any]:
    sig = await db.fetchrow(
        "SELECT count(*) AS n, max(yv.fetched_at) AS m FROM youtube_videos yv"
        " JOIN youtube_channels yc ON yc.channel_id = yv.channel_id WHERE yc.library_id = $1",
        uid(library_id),
    )
    key = (str(library_id), f"{sig['n']}|{sig['m']}")
    hit = _INDEX_CACHE.get(key)
    if hit is not None:
        return hit
    rows = await db.fetch(
        "SELECT yv.youtube_id, yv.title, yv.duration_s FROM youtube_videos yv"
        " JOIN youtube_channels yc ON yc.channel_id = yv.channel_id WHERE yc.library_id = $1"
        " ORDER BY yv.youtube_id",
        uid(library_id),
    )
    cands = [
        Candidate(
            str(r["youtube_id"]),
            str(r["title"]),
            None if r["duration_s"] is None else float(r["duration_s"]),
        )
        for r in rows
    ]
    value = (cands, build_index(cands))
    for stale in [k for k in _INDEX_CACHE if k[0] == key[0]]:
        del _INDEX_CACHE[stale]
    if len(_INDEX_CACHE) >= 8:
        _INDEX_CACHE.pop(next(iter(_INDEX_CACHE)))
    _INDEX_CACHE[key] = value
    return value


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    v = await load_video(db, subject.id)
    if v is None:
        return None
    sync = await db.fetchrow(
        "SELECT output_hash FROM processing_jobs WHERE subject_kind = 'library' AND subject_id = $1"
        " AND stage = 'youtube_sync' AND state = 'done'"
        " AND outcome IN ('changed','unchanged','skipped_unchanged','imported')"
        " ORDER BY id DESC LIMIT 1",
        uid(v.library_id),
    )
    return canonical_hash(
        {
            "filename": v.filename,
            "title": v.title,
            "duration": v.duration_s,
            "sync": None if sync is None else sync["output_hash"],
            "v": LINK_VERSION,
        }
    )


async def _upsert_youtube_video(db: Any, info: VideoInfo) -> None:
    await db.execute(
        "INSERT INTO youtube_videos (youtube_id, channel_id, title, upload_date, date_quality,"
        " duration_s, description, source, fetched_at) VALUES ($1,$2,$3,$4,$5,$6,$7,'info_json',$8)"
        " ON CONFLICT (youtube_id) DO UPDATE SET title = EXCLUDED.title,"
        " upload_date = COALESCE(EXCLUDED.upload_date, youtube_videos.upload_date),"
        " date_quality = CASE WHEN EXCLUDED.upload_date IS NOT NULL THEN 'exact'"
        "   ELSE youtube_videos.date_quality END,"
        " duration_s = COALESCE(EXCLUDED.duration_s, youtube_videos.duration_s),"
        " channel_id = COALESCE(EXCLUDED.channel_id, youtube_videos.channel_id),"
        " description = COALESCE(EXCLUDED.description, youtube_videos.description)",
        info.youtube_id,
        info.channel_id,
        info.title,
        to_date(db, info.upload_date),
        "exact" if info.upload_date else None,
        info.duration_s,
        info.description,
        to_ts(db, now()),
    )
    if info.playlist_id and info.playlist_title:
        await db.execute(
            "INSERT INTO youtube_playlists (playlist_id, channel_id, title, is_real, source,"
            " fetched_at) VALUES ($1,$2,$3,$4,'info_json',$5)"
            " ON CONFLICT (playlist_id) DO UPDATE SET title = EXCLUDED.title",
            info.playlist_id,
            info.channel_id,
            info.playlist_title,
            is_real_playlist(info.playlist_id),
            to_ts(db, now()),
        )
        await db.execute(
            "INSERT INTO youtube_playlist_items (playlist_id, youtube_id, position)"
            " VALUES ($1,$2,$3) ON CONFLICT (playlist_id, youtube_id) DO UPDATE SET"
            " position = COALESCE(EXCLUDED.position, youtube_playlist_items.position)",
            info.playlist_id,
            info.youtube_id,
            info.playlist_index,
        )


def read_sidecar(local_video_path: str) -> VideoInfo | None:
    """The video's own ``<stem>.info.json``, when yt-dlp left one next to it."""
    side = str(Path(local_video_path).with_suffix("")) + ".info.json"
    if not os.path.isfile(side):
        return None
    with open(side, encoding="utf-8", errors="replace") as fh:
        info = parse_info_json(fh.read())
    return info if isinstance(info, VideoInfo) else None


async def _write_link(db: Any, video_id: UUID, match: LinkMatch, mapped: dict[str, Any]) -> None:
    await db.execute(
        "DELETE FROM media_metadata_enrichment WHERE video_id = $1 AND provider = 'youtube'"
        " AND external_id <> $2",
        uid(video_id),
        match.youtube_id,
    )
    user_edit = await db.fetchrow(
        "SELECT 1 AS x FROM media_field_provenance WHERE video_id = $1 AND origin = 'user' LIMIT 1",
        uid(video_id),
    )
    await db.execute(
        "INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped,"
        " confidence, is_accepted, fetched_at) VALUES ($1,$2,'youtube',$3,$4,$5,$6,$7)"
        " ON CONFLICT (video_id, provider, external_id) DO UPDATE SET mapped = EXCLUDED.mapped,"
        " confidence = EXCLUDED.confidence, fetched_at = EXCLUDED.fetched_at",
        str(uuid.uuid4()),
        uid(video_id),
        match.youtube_id,
        dumps(mapped),
        match.confidence,
        user_edit is None,
        to_ts(db, now()),
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    local = ctx.local_path(v.path)
    match: LinkMatch | None = None
    mapped: dict[str, Any] = {}

    info = read_sidecar(local)
    if info is not None:
        await _upsert_youtube_video(db, info)
        match = LinkMatch(info.youtube_id, "info_json")
        mapped = {
            "title": info.title,
            "upload_date": None if info.upload_date is None else info.upload_date.isoformat(),
            "date_quality": "exact" if info.upload_date else None,
        }
    else:
        cands, index = await _candidates(db, v.library_id)
        taken_rows = await db.fetch(
            "SELECT m.external_id AS e FROM media_metadata_enrichment m"
            " JOIN videos x ON x.id = m.video_id"
            " WHERE x.library_id = $1 AND m.provider = 'youtube' AND m.video_id <> $2",
            uid(v.library_id),
            uid(v.id),
        )
        taken = {str(r["e"]) for r in taken_rows}
        stem = v.filename.rsplit(".", 1)[0]
        match = match_candidates(stem, v.duration_s, cands, taken, index=index)
        if match is not None:
            row = await db.fetchrow(
                "SELECT title, upload_date, date_quality FROM youtube_videos WHERE youtube_id = $1",
                match.youtube_id,
            )
            mapped = {
                "title": row["title"],
                "upload_date": None if row["upload_date"] is None else str(row["upload_date"]),
                "date_quality": row["date_quality"],
            }
    if match is None:
        return StageResult(
            output_hash=canonical_hash({"youtube_id": None}),
            summary={"linked": False},
        )
    await _write_link(db, v.id, match, {"link_method": match.method, **mapped})
    return StageResult(
        output_hash=canonical_hash({"youtube_id": match.youtube_id, "method": match.method}),
        summary={"linked": True, "youtube_id": match.youtube_id, "method": match.method},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
