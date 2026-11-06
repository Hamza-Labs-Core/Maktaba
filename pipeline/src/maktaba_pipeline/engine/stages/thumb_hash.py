"""``thumb_hash``: the dHash of a video's thumbnail (plan 06-11 section 3.2).

Episodes of one show usually share branding, so their thumbnails land within a
few bits of each other; the detector uses that as evidence. The thumbnail is the
yt-dlp sidecar (``<stem>.webp / .jpg / .png`` next to the video, else in the folder's
``.thumbs/``: :func:`~maktaba_pipeline.discovery.ytdlp_sidecars.find_sidecar_thumbnail`),
else the poster Maktaba already generated.

Pure analysis: :mod:`maktaba_pipeline.thumbnail.dhash`. This module is the I/O half.
"""

from __future__ import annotations

import asyncio
import os
from pathlib import Path
from typing import Any

from ...db.jobs import Job
from ...discovery.ytdlp_sidecars import THUMBS_DIR, find_sidecar_thumbnail
from ...thumbnail.dhash import DHASH_VERSION, dhash_from_gray, ffmpeg_gray_args
from ..context import StageContext, StageFailure
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..media import ffmpeg, run_media
from ..paths import to_local
from ._common import VideoFacts, load_video, require_video, upsert_signals
from .registry import StageImpl

__all__ = ["IMPL", "find_thumbnail", "fingerprint_doc", "thumbs_dir_source"]


def find_thumbnail(video_local_path: str, poster_path: str | None) -> str | None:
    """The video's sidecar thumbnail (next to it, else in ``.thumbs/``), else the poster,
    else None."""
    sidecar = find_sidecar_thumbnail(video_local_path)
    if sidecar is not None:
        return sidecar
    if poster_path and os.path.isfile(poster_path):
        return poster_path
    return None


def _thumbs_dir_name(video_local_path: str) -> str | None:
    found = find_sidecar_thumbnail(video_local_path)
    if found is None or os.path.basename(os.path.dirname(found)) != THUMBS_DIR:
        return None
    return os.path.basename(found)


async def thumbs_dir_source(v: VideoFacts) -> str | None:
    """The name of the video's thumbnail when it is found in the folder's ``.thumbs/``
    (none next to the video), else None. A few stats, off the event loop."""
    return await asyncio.to_thread(lambda: _thumbs_dir_name(to_local(v.path)))


async def fingerprint_doc(v: VideoFacts, version: int) -> dict[str, Any]:
    """What ``thumb_hash`` and ``thumb_small`` fingerprint: the file's identity, the
    poster they could fall back to, and a thumbnail found in ``.thumbs/``.

    Only a ``.thumbs/`` thumbnail is named. Until 2026-11 the stages looked next to the
    video only, so such a video was answered from the poster; its fingerprint now
    changes and its next job runs. Every other video's fingerprint stays exactly what it
    was, so none of the ~230k done jobs runs again for this change.
    """
    doc: dict[str, Any] = {
        "size": v.size_bytes, "mtime": v.mtime, "poster": v.poster_path, "v": version
    }  # fmt: skip
    thumbs = await thumbs_dir_source(v)
    if thumbs is not None:
        doc["thumbs"] = thumbs
    return doc


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """See :func:`fingerprint_doc`."""
    v = await load_video(db, subject.id)
    if v is None:
        return None
    return canonical_hash(await fingerprint_doc(v, DHASH_VERSION))


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    thumb = find_thumbnail(
        ctx.local_path(v.path), ctx.local_path(v.poster_path) if v.poster_path else None
    )
    dhash: str | None = None
    if thumb is not None:
        async with ctx.nas.timed(thumb):
            res = await run_media(ffmpeg_gray_args(thumb, binary=ffmpeg()), timeout=60.0)
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
        # thumbs_dir: this run looked in .thumbs/ too (the reconciler's one-time sweep
        # leaves such a result alone: engine/reconcile.py)
        summary={
            "dhash": dhash,
            "source": None if thumb is None else Path(thumb).name,
            "thumbs_dir": True,
        },
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
