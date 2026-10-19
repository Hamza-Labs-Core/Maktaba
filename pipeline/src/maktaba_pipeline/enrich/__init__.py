"""Epic 26 — Content Intelligence: the out-of-band ``enrich`` queue.

Web metadata enrichment (Story 26.5) is networked, rate-limited, and
non-deterministic, so — unlike the local ``classify`` stage — it must
**never** sit on the ``videos`` state machine's critical path: a
rate-limited search key or an offline box must still let a video reach
``READY`` (Epic 26 key decision; Story 26.7 D2).

The provider (stub audit #4) uses no outside catalogue: Maktaba's own
intel, and a web search a language model reads when configured.

This package owns that decoupling:

- :mod:`.jobs` — the ``enrich_jobs`` queue (slot 0079): enqueue, claim,
  complete, defer (rate-limit / breaker, no attempt consumed), and
  retry-or-fail (backoff, attempt consumed). Modeled on the
  ``processing_jobs`` claim semantics but on its own table so it runs
  slow without holding a video out of ``READY``.
- :mod:`.gating` — :func:`should_enqueue_enrich`: enrichment is enqueued
  on ``classify`` completion only when ``settings.enrich.enabled`` and at
  least one provider key is configured.
- :mod:`.worker` — the claim → enrich → complete/defer/retry routing, run
  by the engine's ``enrich`` stage (``engine/stages/enrich.py``);
- :mod:`.sweep` — the home's 30 s sweep that turns queued rows into engine
  jobs (so they show in the control centre and never pile up);
- the provider: :mod:`.intel` (what the database knows), :mod:`.internal`
  (the internal suggestion), :mod:`.web` (Google / Ollama search, pages),
  :mod:`.reader` (the model, prompt-injection safe), :mod:`.budget` (daily
  caps), :mod:`.cache`, :mod:`.service` (one video), :mod:`.providers`
  (which backend and model the settings pick), :mod:`.config`
  (``rules.enrich``).

The provider is injected into the worker as an
:class:`~maktaba_pipeline.enrich.worker.EnrichService`, so the routing
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
