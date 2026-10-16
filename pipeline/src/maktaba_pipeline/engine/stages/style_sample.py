"""``style_sample``: production-style metrics of one video (plan 06-11 section 3.2).

Reads a few short windows over the share (ported from the prototype's
``scan_visual.sample_file``) and stores the per-file ``StyleMetrics`` in
``video_signals.style_metrics``. ``rate_style`` turns the samples of a season
into one style; thresholds can change there without re-reading any video.
"""

from __future__ import annotations

import json
from typing import Any

from ...classify.production_style import (
    STYLE_VERSION,
    StreamInfo,
    StyleMetrics,
    WindowSample,
    compute_metrics,
    ffmpeg_sample_args,
    parse_frame_stats,
    parse_stream_info,
    sample_windows,
    split_thumbs,
)
from ...db.jobs import Job
from ..context import StageContext, StageFailure
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..media import FFMPEG, run_media
from ._common import load_video, require_video, upsert_signals
from .registry import StageImpl

__all__ = ["IMPL", "WINDOWS", "WINDOW_S", "sample_file"]

WINDOWS = 4
WINDOW_S = 30.0


async def sample_file(
    ctx: StageContext, path: str, duration_s: float | None
) -> StyleMetrics | None:
    """Run ffmpeg over the file's sample windows; None when nothing decoded."""
    samples: list[WindowSample] = []
    info: StreamInfo | None = None
    for start, length in sample_windows(duration_s, count=WINDOWS, length_s=WINDOW_S):
        async with ctx.nas.timed(path):
            res = await run_media(
                ffmpeg_sample_args(path, start, length, binary=FFMPEG), timeout=300.0
            )
        log = res.text_err
        if res.returncode == 124:  # timed out: count it against the NAS and move on
            ctx.nas.collector.record_timeout()
            continue
        if res.returncode != 0:
            ctx.nas.record_stderr(log)
        if info is None or info.width is None:
            info = parse_stream_info(log)
        frames = parse_frame_stats(log)
        if frames:
            samples.append(WindowSample(length, frames, split_thumbs(res.stdout)))
    if not samples:
        return None
    return compute_metrics(samples, info)


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    v = await load_video(db, subject.id)
    if v is None:
        return None
    return canonical_hash(
        {"size": v.size_bytes, "mtime": v.mtime, "duration": v.duration_s, "v": STYLE_VERSION}
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    metrics = await sample_file(ctx, ctx.local_path(v.path), v.duration_s)
    if metrics is None:
        raise StageFailure("no_frames", "no frames decoded", retryable=True)
    doc = metrics.to_dict()
    await upsert_signals(db, v.id, style_metrics=json.dumps(doc), style_version=STYLE_VERSION)
    return StageResult(output_hash=canonical_hash(doc), summary={"windows": WINDOWS})


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
