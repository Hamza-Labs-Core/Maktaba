"""Epic 26 — Content Intelligence: the out-of-band ``enrich`` queue.

Web metadata enrichment (Story 26.5) is networked, rate-limited, and
non-deterministic, so — unlike the local ``classify`` stage — it must
**never** sit on the ``videos`` state machine's critical path: a
rate-limited TMDb key or an offline box must still let a video reach
``READY`` (Epic 26 key decision; Story 26.7 D2).

This package owns that decoupling:

- :mod:`.jobs` — the ``enrich_jobs`` queue (slot 0079): enqueue, claim,
  complete, defer (rate-limit / breaker, no attempt consumed), and
  retry-or-fail (backoff, attempt consumed). Modeled on the
  ``processing_jobs`` claim semantics but on its own table so it runs
  slow without holding a video out of ``READY``.
- :mod:`.gating` — :func:`should_enqueue_enrich`: enrichment is enqueued
  on ``classify`` completion only when ``settings.enrich.enabled`` and at
  least one provider key is configured.
- :mod:`.worker` — the claim → enrich → complete/defer/retry loop.

The actual provider fetch (Story 26.5) is injected as an
:class:`~maktaba_pipeline.enrich.worker.EnrichService`, so this package
stays unit-testable with no network and no real DB.
"""

from __future__ import annotations

from .gating import EnrichSettings, ProviderKey, should_enqueue_enrich
from .jobs import (
    EnrichJob,
    EnrichJobStatus,
    claim_enrich_job,
    complete_enrich_job,
    defer_enrich_job,
    enqueue_enrich,
    retry_or_fail_enrich_job,
)

__all__ = [
    "EnrichJob",
    "EnrichJobStatus",
    "EnrichSettings",
    "ProviderKey",
    "claim_enrich_job",
    "complete_enrich_job",
    "defer_enrich_job",
    "enqueue_enrich",
    "retry_or_fail_enrich_job",
    "should_enqueue_enrich",
]
