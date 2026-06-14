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
    "EnrichService",
    "ProviderPaused",
    "RateLimited",
    "process_one_enrich_job",
]

log = get_logger(component="enrich_worker")


class ProviderPaused(Exception):
    """Raised by the service when a provider's breaker is open."""


class RateLimited(Exception):
    """Raised by the service when a provider is rate-limited / over cap."""


class EnrichService(Protocol):
    """The Story 26.5 enrichment service, injected into the worker.

    ``enrich_video`` is idempotent via the stored ``external_id`` + the
    shared cache (Story 26.7 resume), and raises :class:`ProviderPaused`
    / :class:`RateLimited` for the defer-class conditions.
    """

    async def enrich_video(self, conn: Conn, video_id: UUID, *, force: bool) -> object: ...


async def process_one_enrich_job(
    conn: Conn,
    service: EnrichService,
) -> EnrichJob | None:
    """Claim and process a single enrich job; return it, or ``None`` if idle.

    This is the unit the worker loop calls per tick. Splitting it out
    keeps the outcome routing (the heart of the story) testable without
    standing up the full claim/wakeup machinery.
    """
    job = await claim_enrich_job(conn)
    if job is None:
        return None
    try:
        await service.enrich_video(conn, job.video_id, force=job.force)
    except ProviderPaused:
        await defer_enrich_job(conn, job, reason="provider_paused")
        log.info("enrich_deferred", video_id=str(job.video_id), reason="provider_paused")
        return job
    except RateLimited:
        await defer_enrich_job(conn, job, reason="rate_limited")
        log.info("enrich_deferred", video_id=str(job.video_id), reason="rate_limited")
        return job
    except Exception as exc:  # noqa: BLE001 — bounded by retry/fail; never reraised onto videos.state
        status = await retry_or_fail_enrich_job(conn, job, error=str(exc))
        log.warning(
            "enrich_error",
            video_id=str(job.video_id),
            attempts=job.attempts + 1,
            outcome=status.value,
            error=str(exc),
        )
        return job
    await complete_enrich_job(conn, job)
    log.info("enrich_done", video_id=str(job.video_id))
    return job
