"""``thumb_hash``: the dHash of a video's thumbnail (plan 06-11 section 3.2).

Episodes of one show usually share branding, so their thumbnails land within a
few bits of each other; the detector uses that as evidence. The thumbnail is the
yt-dlp sidecar (``<stem>.webp / .jpg / .png`` next to the video, else in the folder's
``.thumbs/``: :func:`~maktaba_pipeline.discovery.ytdlp_sidecars.find_sidecar_thumbnail`),
else the poster Maktaba already generated.

Pure analysis: :mod:`maktaba_pipeline.thumbnail.dhash`; the source and the fingerprint's
document: :mod:`maktaba_pipeline.thumbnail.sidecar` (``thumb_small`` shares them). This module
is the I/O half. Epic 54 story 54.36: a stage of ``tv.maktaba.core-media``.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import StageResult, Subject
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.media import ffmpeg, run_media
from maktaba_pipeline.engine.stages._common import load_video, require_video
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.thumbnail.dhash import DHASH_VERSION, dhash_from_gray, ffmpeg_gray_args
from maktaba_pipeline.thumbnail.sidecar import find_thumbnail, fingerprint_doc

from ._signals import upsert_thumb

__all__ = ["IMPL"]


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
    await upsert_thumb(db, v.id, dhash=dhash, version=DHASH_VERSION)
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
