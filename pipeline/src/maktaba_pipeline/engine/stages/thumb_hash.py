"""``thumb_hash``: the dHash of a video's thumbnail (plan 06-11 section 3.2).

Episodes of one show usually share branding, so their thumbnails land within a
few bits of each other; the detector uses that as evidence. The thumbnail is the
yt-dlp sidecar next to the video (``<stem>.webp / .jpg / .png``), else the
poster Maktaba already generated.

Pure analysis: :mod:`maktaba_pipeline.thumbnail.dhash`. This module is the I/O half.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

from ...db.jobs import Job
from ...discovery.ytdlp_sidecars import THUMB_EXTS
from ...thumbnail.dhash import DHASH_VERSION, dhash_from_gray, ffmpeg_gray_args
from ..context import StageContext, StageFailure
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..media import FFMPEG, run_media
from ._common import load_video, require_video, upsert_signals
from .registry import StageImpl

__all__ = ["IMPL", "find_thumbnail"]


def find_thumbnail(video_local_path: str, poster_path: str | None) -> str | None:
    """The sidecar thumbnail next to the video, else the poster, else None."""
    stem = Path(video_local_path).with_suffix("")
    for ext in THUMB_EXTS:
        candidate = str(stem) + ext
        if os.path.isfile(candidate):
            return candidate
    if poster_path and os.path.isfile(poster_path):
        return poster_path
    return None


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the file's identity plus the poster we could fall back to."""
    v = await load_video(db, subject.id)
    if v is None:
        return None
    return canonical_hash(
        {"size": v.size_bytes, "mtime": v.mtime, "poster": v.poster_path, "v": DHASH_VERSION}
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    thumb = find_thumbnail(
        ctx.local_path(v.path), ctx.local_path(v.poster_path) if v.poster_path else None
    )
    dhash: str | None = None
    if thumb is not None:
        async with ctx.nas.timed(thumb):
            res = await run_media(ffmpeg_gray_args(thumb, binary=FFMPEG), timeout=60.0)
        if res.returncode != 0 or not res.stdout:
            ctx.nas.record_stderr(res.text_err)
            raise StageFailure("thumbnail_unreadable", res.text_err[-300:] or "no output")
        try:
            dhash = f"{dhash_from_gray(res.stdout):016x}"
        except ValueError as exc:
            raise StageFailure("thumbnail_unreadable", str(exc)) from exc
    await upsert_signals(db, v.id, thumb_dhash=dhash, thumb_version=DHASH_VERSION)
    return StageResult(
        output_hash=canonical_hash({"dhash": dhash}),
        summary={"dhash": dhash, "source": None if thumb is None else Path(thumb).name},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
