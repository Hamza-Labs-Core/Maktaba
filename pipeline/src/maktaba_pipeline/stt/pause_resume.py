"""Story 3.7 — pause/resume helpers for the transcribe stage.

The pieces:

- :func:`build_resume_prompt` — concatenates the last K segments' text
  to seed the Whisper decoder when resuming. Default K=3.
- :func:`is_pause_due` — checks the cooperative-pause flags on a job
  row; the worker calls this between segment commits.
- :func:`apply_resume_seek` — returns the ffmpeg ``-ss`` seek time
  given the previous ``last_segment_end_sec``; the extract stage
  consumes this directly.

The DB transitions (``running → paused``, ``paused → resuming →
running``) are handled by the existing job-queue helpers in
:mod:`maktaba_pipeline.db.jobs_state`; this module only owns the
transcription-specific seam.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Protocol
from uuid import UUID

__all__ = [
    "DEFAULT_RESUME_PROMPT_K",
    "PauseDecision",
    "ResumePoint",
    "apply_resume_seek",
    "build_resume_prompt",
    "is_pause_due",
    "load_resume_point",
]


DEFAULT_RESUME_PROMPT_K = 3


@dataclass(slots=True, frozen=True)
class PauseDecision:
    """What :func:`is_pause_due` reports back to the worker."""

    pause: bool
    cancel: bool
    reason: str | None


def is_pause_due(
    job_row: dict[str, Any] | None,
) -> PauseDecision:
    """Inspect ``processing_jobs.{pause_requested, cancel_requested}``.

    Returns a :class:`PauseDecision` with the reason filled in. The
    worker reads its own job row (already loaded for the heartbeat
    update) and feeds it here — the function is intentionally pure.
    """
    if job_row is None:
        return PauseDecision(pause=False, cancel=False, reason=None)
    cancel = bool(job_row.get("cancel_requested"))
    pause = bool(job_row.get("pause_requested"))
    if cancel:
        return PauseDecision(pause=False, cancel=True, reason="cancel")
    if pause:
        reason = job_row.get("paused_reason") or "user"
        return PauseDecision(pause=True, cancel=False, reason=reason)
    return PauseDecision(pause=False, cancel=False, reason=None)


@dataclass(slots=True, frozen=True)
class ResumePoint:
    """Snapshot the worker uses to set up a resume."""

    transcript_id: UUID
    last_segment_end_sec: float
    prompt: str
    pinned_language: str | None


class _DBConn(Protocol):
    async def fetchrow(self, sql: str, *args: Any) -> Any: ...

    async def fetch(self, sql: str, *args: Any) -> Any: ...


_LAST_K_SQL = """
SELECT seq, text
  FROM transcript_segments
 WHERE transcript_id = $1
 ORDER BY seq DESC
 LIMIT $2
"""


async def load_resume_point(
    db: _DBConn,
    *,
    transcript_id: UUID,
    last_segment_end_sec: float,
    pinned_language: str | None,
    k: int = DEFAULT_RESUME_PROMPT_K,
) -> ResumePoint:
    """Read the last K committed segments, build the resume prompt."""
    rows = await db.fetch(_LAST_K_SQL, transcript_id, k)
    # rows come back newest-first; reverse so the prompt reads forward.
    seg_texts = list(reversed([str(r["text"]) for r in rows]))
    prompt = build_resume_prompt(seg_texts)
    return ResumePoint(
        transcript_id=transcript_id,
        last_segment_end_sec=last_segment_end_sec,
        prompt=prompt,
        pinned_language=pinned_language,
    )


def build_resume_prompt(last_segments: list[str]) -> str:
    """Produce a prompt the decoder can use to seed the resume.

    Whisper's ``initial_prompt`` is a free-form text; we concatenate
    with single spaces and trim. Story 3.7 EC: we keep using the
    pre-pause language and disable autodetect on resume so a re-detect
    at a mid-sentence boundary doesn't flip language.
    """
    if not last_segments:
        return ""
    return " ".join(s.strip() for s in last_segments if s and s.strip())[-1024:]


def apply_resume_seek(last_segment_end_sec: float) -> float:
    """Return the seek offset for ffmpeg's ``-ss``.

    Mirrors the Story 2.3 EC1 0.5-s lead-in subtraction so VBR/VFR
    audio resumes exactly. The caller (extract stage) is responsible
    for discarding the lead-in until the first PCM sample whose PTS
    exceeds the requested resume point.
    """
    return max(0.0, last_segment_end_sec)
