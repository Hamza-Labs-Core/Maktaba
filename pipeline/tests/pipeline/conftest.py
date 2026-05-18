"""Shared fixtures for the real stage-adapter handler tests.

The adapters in :mod:`maktaba_pipeline.handlers` are deliberately thin:
they look up the prerequisites the prior stage produced, call the
existing (well-unit-tested) Epic 1-6 library function, and flip the
job ``done`` / ``failed`` via :mod:`maktaba_pipeline.db.jobs_state`.

To test them against the *same* DB contract the libraries already
expect, this fixture builds a ``StageDB`` fake that reuses the audio
test suite's :class:`FakeAudioDB` (the canonical fake for
``commit_probe`` / ``enqueue`` / ``advance_after_stage``) and layers a
``videos.path`` lookup on top — the one extra column the PROBE adapter
needs that no library function exposed before.
"""

from __future__ import annotations

from typing import Any
from uuid import UUID

import pytest

from tests.audio._fake_audio_db import FakeAudioDB, _Row

__all__ = ["StageDB", "make_job"]


class StageDB(FakeAudioDB):
    """:class:`FakeAudioDB` plus a ``videos.path`` / ``content_hash`` projection.

    The PROBE adapter resolves the on-disk source file with
    ``SELECT path FROM videos WHERE id = $1`` before shelling out to
    ffprobe. The EXTRACT adapter additionally needs the video's
    ``content_hash`` (the audio-cache key). ``FakeAudioDB`` only
    modelled ``state`` for ``videos``, so we extend its dispatch with
    those two extra columns; the ``audio_tracks`` read-back, the
    ``audio_cache`` UPSERT, and the ``last_extracted_at`` stamp are all
    handled by the shared :class:`FakeAudioDB` (the canonical
    ``commit_extract`` fake) so the adapter and the library function
    exercise the *same* in-memory tables.
    """

    video_paths: dict[UUID, str]
    video_hashes: dict[UUID, str]

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        super().__init__(*args, **kwargs)
        self.video_paths = {}
        self.video_hashes = {}

    def add_video(
        self,
        *,
        state: str = "discovered",
        library_id: UUID | None = None,
        path: str = "/library/clip.mkv",
        content_hash: str = "a" * 64,
    ) -> UUID:
        vid = super().add_video(state=state, library_id=library_id)
        self.video_paths[vid] = path
        self.video_hashes[vid] = content_hash
        return vid

    def _dispatch(self, s: str, args: tuple[Any, ...], *, many: bool) -> Any:
        # EXTRACT adapter: source path + cache key in one SELECT.
        if s.startswith("SELECT path, content_hash FROM videos"):
            vid = args[0]
            path = self.video_paths.get(vid)
            chash = self.video_hashes.get(vid)
            if path is None or chash is None:
                return None
            return _Row({"path": path, "content_hash": chash})

        if s.startswith("SELECT path FROM videos"):
            vid = args[0]
            path = self.video_paths.get(vid)
            if path is None:
                return None
            return _Row({"path": path})

        # jobs_state.mark_done — terminal success transition.
        if s.startswith("UPDATE processing_jobs SET state = 'done'"):
            job = self.processing_jobs.get(int(args[0]))
            if job is None or job.state not in {"claimed", "running", "resuming"}:
                return None
            job.state = "done"
            job.finished_at = self._now()
            return _Row({"id": job.id, "state": "done"})

        # jobs_state.mark_failed_or_retry — read attempts/max_attempts.
        if s.startswith("SELECT attempts, max_attempts FROM processing_jobs"):
            job = self.processing_jobs.get(int(args[0]))
            if job is None:
                return None
            return _Row(
                {
                    "attempts": getattr(job, "attempts", 1),
                    "max_attempts": getattr(job, "max_attempts", 3),
                }
            )

        # jobs_state.mark_failed_or_retry — write the failed/pending row.
        if s.startswith("UPDATE processing_jobs SET state = $2") or s.startswith(
            "UPDATE processing_jobs SET state = ?"
        ):
            if self.dialect == "postgres":
                # (job_id, new_state, not_before, err_json, finished_at)
                job_id, new_state = args[0], args[1]
                not_before = args[2]
                err_json = args[3]
            else:
                # (new_state, not_before, err_json, finished_at, job_id)
                new_state, not_before = args[0], args[1]
                err_json = args[2]
                job_id = args[4]
            job = self.processing_jobs.get(int(job_id))
            if job is None or job.state not in {"claimed", "running", "resuming"}:
                return None
            job.state = str(new_state)
            job.error = err_json
            return _Row(
                {"id": job.id, "state": str(new_state), "not_before": not_before}
            )

        return super()._dispatch(s, args, many=many)


def make_job(
    *,
    job_id: int,
    video_id: UUID | None,
    stage: Any,
    library_id: UUID | None = None,
    payload: dict[str, Any] | None = None,
) -> Any:
    """Build a minimal :class:`~maktaba_pipeline.db.jobs.Job`.

    Only the fields the adapters touch are meaningful; the rest take
    schema-shaped defaults so the frozen dataclass constructs cleanly.

    Slot 0058: per-video stages pass ``video_id`` (``library_id`` left
    ``None``); a SCAN job passes ``library_id`` with ``video_id=None``.
    The claim loop hydrates ``payload`` from the row's JSON; tests that
    drive a payload-carrying stage (TRANSCRIBE) pass it explicitly so
    the adapter sees the same shape EXTRACT enqueued.
    """
    from datetime import UTC, datetime

    from maktaba_pipeline.db.jobs import Job, JobState

    now = datetime.now(UTC)
    return Job(
        id=job_id,
        video_id=video_id,
        library_id=library_id,
        stage=stage,
        state=JobState.CLAIMED,
        priority=100,
        attempts=1,
        max_attempts=3,
        claimed_by="w-test",
        claimed_at=now,
        last_heartbeat_at=now,
        not_before=None,
        error=None,
        total_duration_seconds=None,
        processed_seconds=0.0,
        segments_completed=0,
        last_segment_end_sec=0.0,
        estimated_remaining_sec=None,
        realtime_factor=None,
        progress_updated_at=None,
        pause_requested=False,
        cancel_requested=False,
        paused_at=None,
        paused_at_sec=None,
        paused_reason=None,
        resumed_at=None,
        resume_count=0,
        metrics=None,
        payload=payload,
        created_at=now,
        finished_at=None,
    )


@pytest.fixture
def stage_db() -> StageDB:
    return StageDB(dialect="postgres")
