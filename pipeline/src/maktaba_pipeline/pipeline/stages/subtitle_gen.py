"""``subtitle_gen`` stage handler (Story 4.1).

Reads the active transcript for a video out of ``transcript_segments_v``,
shapes the segments into cues, writes the SRT+VTT pair atomically into
``<library>/.maktaba/subs/``, attempts an alias copy beside the source
video for each format, and upserts a row per format into
``subtitle_files``.

The handler is dialect-agnostic: it takes a :class:`DBConn` whose
SQL placeholders are ``$N`` (asyncpg style). The connection wrapper
(Story 1.5) rewrites them to ``?`` for SQLite. The UPSERT uses the
partial-index conflict target ``(video_id, format, language) WHERE
NOT is_external AND NOT is_embedded`` which is present on both
dialects (slot 0015).
"""

from __future__ import annotations

import json
from contextlib import AbstractAsyncContextManager
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Protocol
from uuid import UUID

from ...media.subtitles import (
    Cue,
    CueShaper,
    Segment,
    alias_copy,
    alias_path_for,
    canonical_subtitle_path,
    default_shaper,
    ensure_sidecar_dirs,
    write_atomic_pair,
    write_srt,
    write_vtt,
)

__all__ = ["run_subtitle_gen_stage"]


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class _Logger(Protocol):
    def info(self, event: str, **kwargs: Any) -> Any: ...

    def warning(self, event: str, **kwargs: Any) -> Any: ...


class DBConn(Protocol):
    """Connection shape the stage handler needs."""

    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None: ...

    async def fetch(self, sql: str, *args: Any) -> list[_Row]: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


_SELECT_SEGMENTS_SQL = """
SELECT transcript_id,
       language_code,
       segment_id,
       seq,
       start_sec,
       end_sec,
       text,
       speaker
  FROM transcript_segments_v
 WHERE video_id = $1
 ORDER BY seq
"""

# UPSERT into subtitle_files. The conflict target uses the partial
# unique index from slot 0015 which is present on both dialects.
_UPSERT_SQL = """
INSERT INTO subtitle_files
       (video_id, transcript_id, format, language, path,
        is_external, is_embedded, size_bytes, mtime_ns, metadata)
VALUES ($1, $2, $3, $4, $5, false, false, $6, $7, $8)
ON CONFLICT (video_id, format, language)
   WHERE is_external = false AND is_embedded = false
DO UPDATE SET
       transcript_id = EXCLUDED.transcript_id,
       path          = EXCLUDED.path,
       size_bytes    = EXCLUDED.size_bytes,
       mtime_ns      = EXCLUDED.mtime_ns,
       metadata      = EXCLUDED.metadata
"""


@dataclass(slots=True, frozen=True)
class _SegmentRowState:
    """Aggregated transcript metadata observed while iterating segments."""

    transcript_id: int | None
    language_code: str | None
    segments: list[Segment]


async def _load_segments(db: DBConn, video_id: UUID) -> _SegmentRowState:
    """Fetch and decode every segment for ``video_id`` from the view."""
    rows = await db.fetch(_SELECT_SEGMENTS_SQL, video_id)
    if not rows:
        return _SegmentRowState(transcript_id=None, language_code=None, segments=[])

    transcript_id = rows[0]["transcript_id"]
    language_code = rows[0]["language_code"]
    segments: list[Segment] = []
    for row in rows:
        segments.append(
            Segment(
                seq=int(row["seq"]),
                start_sec=float(row["start_sec"]),
                end_sec=float(row["end_sec"]),
                text=str(row["text"]),
                speaker=row["speaker"] if row["speaker"] is not None else None,
            )
        )
    return _SegmentRowState(
        transcript_id=int(transcript_id) if transcript_id is not None else None,
        language_code=str(language_code) if language_code is not None else None,
        segments=segments,
    )


def _file_stat(path: Path) -> tuple[int, int]:
    """Return (size_bytes, mtime_ns) for an existing path."""
    st = path.stat()
    return (st.st_size, st.st_mtime_ns)


async def run_subtitle_gen_stage(
    *,
    db: DBConn,
    video_id: UUID,
    library_root: Path,
    content_hash: str,
    source_video: Path,
    log: _Logger,
    shaper: CueShaper | None = None,
) -> tuple[str, dict[str, Any]]:
    """Run the ``subtitle_gen`` stage for one video.

    Returns ``(outcome_str, summary_dict)``. ``outcome_str`` is the
    string the orchestrator threads into ``advance_after_stage``:
    ``"ok"`` when both files landed, ``"partial"`` when there were
    no segments to render (the transcript is empty or missing).

    The handler does *not* call ``advance_after_stage`` itself; the
    runner is responsible for that so it can also handle exceptions
    consistently with other stages.
    """
    state = await _load_segments(db, video_id)
    if not state.segments:
        log.info(
            "subtitle_gen_no_segments",
            video_id=str(video_id),
            content_hash=content_hash,
        )
        return ("partial", {"reason": "no_segments"})

    language = state.language_code or "und"
    active_shaper = shaper if shaper is not None else default_shaper()
    cues: list[Cue] = list(active_shaper.shape(state.segments, language=language))

    srt_bytes = write_srt(cues)
    vtt_bytes = write_vtt(cues)

    maktaba_dir = ensure_sidecar_dirs(library_root)
    tmp_dir = maktaba_dir / ".tmp"

    srt_path = canonical_subtitle_path(library_root, content_hash, language, "srt")
    vtt_path = canonical_subtitle_path(library_root, content_hash, language, "vtt")

    write_atomic_pair(
        srt_path, srt_bytes,
        vtt_path, vtt_bytes,
        tmp_dir=tmp_dir,
    )

    # Best-effort alias copy beside the source video. Failures here
    # are warnings, not errors — the canonical artefact is already
    # safely on disk.
    aliases: dict[str, bool] = {}
    for fmt, canonical in (("srt", srt_path), ("vtt", vtt_path)):
        alias = alias_path_for(source_video, language, fmt)
        aliases[fmt] = alias_copy(canonical, alias, log=log)

    # Upsert one row per format. Both rows share the same
    # transcript_id and language but differ in path/size.
    for fmt, canonical in (("srt", srt_path), ("vtt", vtt_path)):
        size, mtime_ns = _file_stat(canonical)
        metadata = {
            "alias_created": aliases[fmt],
            "shaper": type(active_shaper).__name__,
        }
        await db.execute(
            _UPSERT_SQL,
            video_id,
            state.transcript_id,
            fmt,
            language,
            str(canonical),
            size,
            mtime_ns,
            json.dumps(metadata),
        )

    log.info(
        "subtitle_gen_ok",
        video_id=str(video_id),
        content_hash=content_hash,
        language=language,
        cues=len(cues),
        srt_path=str(srt_path),
        vtt_path=str(vtt_path),
        alias_srt=aliases["srt"],
        alias_vtt=aliases["vtt"],
    )

    return (
        "ok",
        {
            "srt": str(srt_path),
            "vtt": str(vtt_path),
            "cues": len(cues),
            "language": language,
            "alias_srt": aliases["srt"],
            "alias_vtt": aliases["vtt"],
        },
    )
