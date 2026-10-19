"""``chapter_thumbs``: one frame per chapter start, after ``chapter_infer``.

The poster and the sprite do not depend on chapters: ``thumbnail`` makes them as soon as
``content_hash`` has read the file. Only the chapter frames do, so this stage redraws
just those (one keyframe seek each, :func:`~maktaba_pipeline.thumbnail.generator.
generate_chapter_thumbs`) into the video's thumbnail cache, and removes the frames of
chapters that no longer exist. A video without chapters has none.

The input fingerprint is the file's identity plus every chapter's ``(seq, start)``,
whatever its source (embedded, manual or inferred).
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from pathlib import Path
from typing import Any

from ...db.jobs import Job
from ...thumbnail.generator import (
    DEFAULT_CONFIG,
    generate_chapter_thumbs,
    thumbnail_dir_for,
)
from ..context import StageContext, StageFailure
from ..graph import CHAPTER_THUMBS_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import uid
from ._common import load_video, require_video
from .registry import StageImpl

__all__ = ["IMPL", "reset_frame_runner", "set_frame_runner"]

_CHAPTERS_SQL = "SELECT seq, start_sec FROM chapters WHERE video_id = $1 ORDER BY seq"

FrameRunner = Callable[[list[str]], Awaitable[None]]
#: runs one ffmpeg command line (None: the generator's own); tests swap in a fake
_runner: FrameRunner | None = None
#: the cache root (None: ``~/.maktaba/cache/thumbnails``); tests point it at a tmp dir
_root: str | None = None


def set_frame_runner(runner: FrameRunner | None, root: str | None = None) -> None:
    global _runner, _root
    _runner, _root = runner, root


def reset_frame_runner() -> None:
    set_frame_runner(None, None)


async def _chapters(db: Any, video_id: Any) -> list[tuple[int, float]]:
    rows = await db.fetch(_CHAPTERS_SQL, uid(video_id))
    return [(int(r["seq"]), round(float(r["start_sec"]), 3)) for r in rows]


async def _content_hash(db: Any, video_id: Any) -> str | None:
    h = await db.fetchval("SELECT content_hash FROM videos WHERE id = $1", uid(video_id))
    return None if h is None else str(h)


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    v = await load_video(db, subject.id)
    if v is None:
        return None
    return canonical_hash(
        {
            "hash": await _content_hash(db, v.id),
            "chapters": await _chapters(db, v.id),
            "v": CHAPTER_THUMBS_VERSION,
        }
    )


def _drop_stale(out_dir: Path, keep: set[int]) -> int:
    """Remove ``chapter-<seq>.<ext>`` frames of chapters that are gone."""
    removed = 0
    for f in out_dir.glob(f"chapter-*.{DEFAULT_CONFIG.image_format}"):
        seq = f.stem.removeprefix("chapter-")
        if seq.isdigit() and int(seq) not in keep:
            f.unlink(missing_ok=True)
            removed += 1
    return removed


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    content_hash = await _content_hash(db, v.id)
    if content_hash is None:
        raise StageFailure("video_unhashed", "the file has no content hash yet", retryable=False)
    chapters = await _chapters(db, v.id)
    out_dir = thumbnail_dir_for(content_hash, root=_root)
    made: dict[int, Path] = {}
    if chapters:
        src = ctx.local_path(v.path)
        await ctx.progress("frames", 0, len(chapters), force=True)
        try:
            made = await generate_chapter_thumbs(
                src,
                chapters=chapters,
                content_hash=content_hash,
                root=_root,
                runner=_runner,
            )
        except Exception as exc:  # ThumbnailError and I/O: the share can come back
            raise StageFailure("chapter_frames_failed", str(exc)[-300:]) from exc
        await ctx.progress("frames", len(chapters), len(chapters), force=True)
    removed = _drop_stale(out_dir, {seq for seq, _ in chapters}) if out_dir.is_dir() else 0
    return StageResult(
        output_hash=canonical_hash({"chapters": chapters, "hash": content_hash}),
        summary={"frames": len(made), "removed": removed},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
