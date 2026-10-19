"""``thumb_small``: a 320 px copy of a video's thumbnail in the app's media cache.

Ported from mediaintel's ``thumbs.py``. Review pages, the control centre and the
web read the same small file, so a thumbnail is read from the share once. The
copy lives in the existing thumbnail cache, next to the poster the ``thumbnail``
stage makes: ``~/.maktaba/cache/thumbnails/<content_hash>/small.jpg``
(:func:`maktaba_pipeline.thumbnail.thumbnail_dir_for`).

The source is the yt-dlp sidecar next to the video, else the poster (the same
choice ``thumb_hash`` makes). ffmpeg writes the JPEG to stdout and the stage
renames a finished temporary file into place, so the cache never holds a
half-written copy. The input fingerprint is the file's identity plus the poster
path, so an unchanged video is never read again; a copy found on disk from an
earlier run is adopted without a read.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

from ...db.jobs import Job
from ...thumbnail.generator import thumbnail_dir_for
from ..context import StageContext, StageFailure
from ..graph import THUMB_SMALL_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..media import FFMPEG, run_media
from ..sql import uid
from ._common import load_video, require_video
from .content_hash import is_pending
from .registry import StageImpl
from .thumb_hash import find_thumbnail

__all__ = [
    "IMPL",
    "SMALL_NAME",
    "THUMB_SMALL_VERSION",
    "WIDTH",
    "small_args",
    "small_path",
    "staged_path",
]

WIDTH = 320
SMALL_NAME = "small.jpg"

#: the cache root (``None``: ``~/.maktaba/cache/thumbnails``); tests point it elsewhere
ROOT: str | None = os.environ.get("MAKTABA_THUMBNAIL_ROOT") or None


def small_path(content_hash: str) -> Path:
    """Where a video's small copy lives (exists or not)."""
    return thumbnail_dir_for(content_hash, root=ROOT) / SMALL_NAME


def staged_path(video_id: Any) -> Path:
    """Where the mediaintel import leaves a video's small copy while its real hash is
    still pending (the final folder is named after that hash); the stage moves it in."""
    return thumbnail_dir_for("imported", root=ROOT) / f"{video_id}.jpg"


def small_args(src: str, *, binary: str = FFMPEG) -> list[str]:
    """One frame of ``src`` scaled to ``WIDTH`` px, JPEG on stdout."""
    return [
        binary, "-v", "error", "-nostdin", "-i", src,
        "-vf", f"scale={WIDTH}:-2", "-frames:v", "1", "-q:v", "5",
        "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1",
    ]  # fmt: skip


async def _content_hash(db: Any, video_id: Any) -> str:
    row = await db.fetchrow("SELECT content_hash FROM videos WHERE id = $1", uid(video_id))
    if row is None or row["content_hash"] is None:
        raise StageFailure(
            "video_missing", f"video {video_id} has no content hash", retryable=False
        )
    return str(row["content_hash"])


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the file's identity plus the poster it could fall back to."""
    v = await load_video(db, subject.id)
    if v is None:
        return None
    return canonical_hash(
        {"size": v.size_bytes, "mtime": v.mtime, "poster": v.poster_path, "v": THUMB_SMALL_VERSION}
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    if is_pending(v.metadata):
        # imported without reading the file: content_hash queues this stage again once
        # the real hash (the cache folder's name) is known
        return StageResult(
            output_hash=canonical_hash({"small": None, "waiting": "content_hash"}),
            outcome="waiting_inputs",
            summary={"small": None, "waiting": "content_hash"},
        )
    dst = small_path(await _content_hash(db, v.id))
    staged = staged_path(v.id)
    if staged.is_file() and staged.stat().st_size > 0 and not dst.is_file():
        dst.parent.mkdir(parents=True, exist_ok=True)
        staged.replace(dst)  # the import's copy: no read of the share
    if dst.is_file() and dst.stat().st_size > 0:  # made by an earlier run or imported
        staged.unlink(missing_ok=True)
        return StageResult(
            output_hash=canonical_hash({"small": dst.name, "size": dst.stat().st_size}),
            summary={"small": str(dst), "adopted": True},
        )
    src = find_thumbnail(
        ctx.local_path(v.path), ctx.local_path(v.poster_path) if v.poster_path else None
    )
    if src is None:
        return StageResult(
            output_hash=canonical_hash({"small": None}),
            summary={"small": None, "reason": "no_thumbnail"},
        )
    async with ctx.nas.timed(src):  # at most nas_read reads at once, as mediaintel's 2
        res = await run_media(small_args(src), timeout=60.0)
    if res.returncode != 0 or not res.stdout:
        ctx.nas.record_stderr(res.text_err)
        raise StageFailure("thumbnail_unreadable", res.text_err[-300:] or "no image")
    dst.parent.mkdir(parents=True, exist_ok=True)
    tmp = dst.with_name(dst.stem + ".part.jpg")
    tmp.write_bytes(res.stdout)
    tmp.replace(dst)  # never a half-written copy under the real name
    return StageResult(
        output_hash=canonical_hash({"small": dst.name, "size": len(res.stdout)}),
        summary={"small": str(dst), "source": Path(src).name, "bytes": len(res.stdout)},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
