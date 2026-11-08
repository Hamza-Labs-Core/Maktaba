"""``probe`` on the engine (Epic 54 story 54.36 §2): read a video's streams with ffprobe and
save them.

Before, ``probe`` was a legacy handler (``handlers.probe_handler``): it marked its own job
done, and the engine then hashed the video's length and frame size
(``legacy._probe_output``) so the declared edges (``thumb_hash``, ``link``, ``detect``)
could fire. Now the stage returns that same hash, so the job's output and its outcome are
what they were; its summary (new) says how many audio tracks it found and the video's
state. The ffprobe call, the saved facts (``commit_probe``: media info, audio tracks, the
video's state) and the failure kinds are the handler's, unchanged. On the engine the probe
never queues ``extract`` (the engine's transcribe asks for it when its backend reads a
file), as before."""

from __future__ import annotations

from typing import Any, cast

from ...audio.probe import _ProbeDB, commit_probe
from ...audio.probe import probe as ffprobe
from ...db.jobs import Job
from ...log import get_logger
from ..context import StageContext, StageFailure
from ..graph import StageResult
from ..paths import to_local
from .legacy import _probe_output
from .registry import StageImpl

__all__ = ["IMPL"]

_log = get_logger()


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    if job.video_id is None:
        # impossible under the per-video scope check; a re-run cannot invent the video
        raise StageFailure(
            "probe_missing_source", f"probe job {job.id} has no video_id", retryable=False
        )
    row = await db.fetchrow("SELECT path FROM videos WHERE id = $1", job.video_id)
    if row is None or row["path"] is None:
        raise StageFailure(
            "probe_missing_source",
            f"no videos.path for video_id={job.video_id}",
            retryable=False,
        )
    try:
        result = await ffprobe(to_local(str(row["path"])))  # this host's mount of the NAS
        state = await commit_probe(
            cast("_ProbeDB", db),
            video_id=job.video_id,
            result=result,
            chain_extract=False,
        )
    except Exception as exc:  # ffprobe, the file, a write: another run may succeed
        raise StageFailure("probe_failed", str(exc), retryable=True) from exc
    # outcome None: changed when the length or frame size differs from the last success,
    # as the legacy bookkeeping decided it
    return StageResult(
        output_hash=await _probe_output(db, job.subject),
        summary={"audio_tracks": len(result.audio), "state": state},
    )


# no input fingerprint: a queued probe always reads the file, as the legacy stage did
IMPL = StageImpl(run=run)
