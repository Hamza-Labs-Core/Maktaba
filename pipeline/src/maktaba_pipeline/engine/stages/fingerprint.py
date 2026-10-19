"""``fingerprint``: audio intro / end-song chromaprint plus frame dHashes (plan 06-11 D12).

Reads part of the video over the share (seven opens: two audio windows, five
frames), so it only runs for the videos ``detect`` / ``fp_gate`` ask for, in
the ``nas_read`` pool. A partial read (a busy NAS failing some opens over SMB)
keeps what was read, marks the row ``fp_partial`` and fails with cause
``nas_partial_read`` so the retry fills the gap; failures never propagate.

Pure analysis: :mod:`maktaba_pipeline.audio.chromaprint` and
:mod:`maktaba_pipeline.thumbnail.dhash`.
"""

from __future__ import annotations

from typing import Any

from ...audio.chromaprint import encode, ffmpeg_chromaprint_args, parse_raw, windows
from ...db.jobs import Job
from ...thumbnail.dhash import (
    dhashes_from_frames,
    ffmpeg_frame_args,
    frame_times,
)
from ..context import StageContext, StageFailure
from ..graph import SIG_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..media import FFMPEG, run_media
from ._common import load_video, require_video, upsert_signals
from .registry import StageImpl

__all__ = ["IMPL", "incomplete"]


def incomplete(
    duration_s: float, head: str | None, tail: str | None, frames: str, unread: int | None = None
) -> str | None:
    """Why a fingerprint is partial, or None (port of ``signatures._incomplete``).

    ``unread``: frames ffmpeg could not read. A flat frame (black, a fade) is read but
    carries no hash, so fewer hashes than frame times is not a failed read; without
    ``unread`` (a stored fingerprint) only the audio windows are judged."""
    missing = [w for w, _, _ in windows(duration_s) if not (head if w == "head" else tail)]
    if unread:
        missing.append(f"{unread} frames")
    return f"read failed: {', '.join(missing)}" if missing else None


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    v = await load_video(db, subject.id)
    if v is None:
        return None
    return canonical_hash(
        {"size": v.size_bytes, "mtime": v.mtime, "duration": v.duration_s, "v": SIG_VERSION}
    )


async def read_signature(
    ctx: StageContext, path: str, duration_s: float
) -> tuple[str | None, str | None, str, int]:
    """``(audio head, audio tail, frame hashes, frames not read)`` of one video."""
    audio: dict[str, str | None] = {"head": None, "tail": None}
    for which, start, length in windows(duration_s):
        async with ctx.nas.timed(path):
            res = await run_media(
                ffmpeg_chromaprint_args(path, start, length, binary=FFMPEG), timeout=180.0
            )
        if res.returncode == 0:
            fp = parse_raw(res.stdout)
            audio[which] = encode(fp) if fp else None
        else:
            ctx.nas.record_stderr(res.text_err)
    raw = b""
    unread = 0
    for t in frame_times(duration_s):
        res = await run_media(ffmpeg_frame_args(path, t, binary=FFMPEG), timeout=60.0)
        if res.returncode == 0 and len(res.stdout) >= 72:
            raw += res.stdout[:72]
        else:
            unread += 1
            if res.returncode != 0:
                ctx.nas.record_stderr(res.text_err)
    frames = ",".join(f"{h:016x}" for h in dhashes_from_frames(raw))
    return audio["head"], audio["tail"], frames, unread


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    if not v.duration_s:
        # the length is probed first; probe's output change re-queues the requesters
        return StageResult(output_hash=canonical_hash("no-duration"), outcome="waiting_inputs")
    head, tail, frames, unread = await read_signature(ctx, ctx.local_path(v.path), v.duration_s)
    why = incomplete(v.duration_s, head, tail, frames, unread)
    await upsert_signals(
        db,
        v.id,
        audio_head=head,
        audio_tail=tail,
        frame_hashes=frames,
        fp_version=SIG_VERSION,
        fp_partial=why is not None,
    )
    if why is not None:
        raise StageFailure("fp_partial", why, retryable=True)
    return StageResult(
        output_hash=canonical_hash({"head": head, "tail": tail, "frames": frames}),
        summary={"audio": [head is not None, tail is not None], "frames": len(frames.split(","))},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
