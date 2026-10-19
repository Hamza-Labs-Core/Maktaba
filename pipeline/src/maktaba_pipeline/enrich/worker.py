"""The out-of-band enrich worker loop (Story 26.7 D2/§3).

A claim → enrich → complete/defer/retry loop over ``enrich_jobs``,
decoupled from ``processing_jobs`` so it can run slow without holding a
video out of ``READY``. The provider fetch itself (Story 26.5) is
injected as an :class:`EnrichService`, so the loop has no network or DB
of its own beyond the queue helpers and is unit-testable with a stub.

Outcome routing mirrors the plan:

- success → :func:`complete_enrich_job` (+ optional post-hooks)
- :class:`ProviderPaused` / :class:`RateLimited` → :func:`defer_enrich_job`
  (no attempt consumed)
- any other error → :func:`retry_or_fail_enrich_job` (attempt consumed,
  backoff; never touches ``videos.state``)
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta
from typing import Protocol
from uuid import UUID

from ..log import get_logger
from .jobs import (
    Conn,
    EnrichJob,
    claim_enrich_job,
    complete_enrich_job,
    defer_enrich_job,
    retry_or_fail_enrich_job,
)

__all__ = [
    "EnrichRun",
    "EnrichService",
    "ProviderPaused",
    "RateLimited",
    "process_one_enrich_job",
]

log = get_logger(component="enrich_worker")


class ProviderPaused(Exception):
    """Raised by the service when a provider's breaker is open."""

    def __init__(self, reason: str = "provider_paused", *, retry_after_sec: float = 900.0):
        super().__init__(reason)
        self.reason = reason
        self.retry_after_sec = retry_after_sec


class RateLimited(Exception):
    """Raised by the service when a provider is rate-limited / over a daily cap. What
    the service could do without it (the internal suggestion) is already written."""

    def __init__(self, reason: str = "rate_limited", *, retry_after_sec: float = 900.0):
        super().__init__(reason)
        self.reason = reason
        self.retry_after_sec = retry_after_sec


class EnrichService(Protocol):
    """The Story 26.5 enrichment service, injected into the worker.

    ``enrich_video`` is idempotent via the stored ``external_id`` + the
    shared cache (Story 26.7 resume), and raises :class:`ProviderPaused`
    / :class:`RateLimited` for the defer-class conditions.
    """

    async def enrich_video(
        self,
        conn: Conn,
        video_id: UUID,
        *,
        force: bool,
        query: str | None = None,
        year: int | None = None,
    ) -> object: ...


@dataclass
class EnrichRun:
    """What :func:`process_one_enrich_job` did with the job it claimed."""

    job: EnrichJob
    #: ``done`` | ``deferred`` | ``pending`` (retry later) | ``failed``
    status: str
    result: object = None
    reason: str = ""
    retry_after_sec: float = 0.0


async def process_one_enrich_job(
    conn: Conn,
    service: EnrichService,
    *,
    video_id: UUID | None = None,
) -> EnrichRun | None:
    """Claim and process a single enrich job; return what happened, or ``None`` if idle.

    This is the unit the engine's ``enrich`` stage runs (``video_id``: that video's
    job only). Splitting it out keeps the outcome routing (the heart of the story)
    testable without standing up the full claim/wakeup machinery.
    """
    job = await claim_enrich_job(conn, video_id=video_id)
    if job is None:
        return None
    try:
        result = await service.enrich_video(
            conn, job.video_id, force=job.force, query=job.query, year=job.year
        )
    except (ProviderPaused, RateLimited) as exc:
        await defer_enrich_job(
            conn, job, reason=exc.reason, delay=timedelta(seconds=exc.retry_after_sec)
        )
        log.info("enrich_deferred", video_id=str(job.video_id), reason=exc.reason)
        return EnrichRun(job, "deferred", reason=exc.reason, retry_after_sec=exc.retry_after_sec)
    except Exception as exc:  # noqa: BLE001 — bounded by retry/fail; never reraised onto videos.state
        status = await retry_or_fail_enrich_job(conn, job, error=str(exc))
        log.warning(
            "enrich_error",
            video_id=str(job.video_id),
            attempts=job.attempts + 1,
            outcome=status.value,
            error=str(exc),
        )
        return EnrichRun(job, status.value, reason=str(exc))
    await complete_enrich_job(conn, job)
    log.info("enrich_done", video_id=str(job.video_id))
    return EnrichRun(job, "done", result=result)
