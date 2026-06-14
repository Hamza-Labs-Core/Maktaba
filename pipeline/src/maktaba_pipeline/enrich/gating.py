"""Enrich-enqueue gating (Story 26.7 D4).

On ``classify`` completion the pipeline fire-and-forgets an enrich job —
but only when enrichment is actually possible. Two conditions must hold
(Story 26.7 AC):

1. the library opted in (``settings.enrich.enabled``); and
2. at least one provider key is configured (a keyless box has nothing to
   call, so enqueuing would just churn deferred jobs).

The check is a pure function over small value objects so the wiring
(:mod:`maktaba_pipeline.enrich.worker` / the classify stage) and its
tests never need a live settings store or secret backend.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass

__all__ = ["EnrichSettings", "ProviderKey", "should_enqueue_enrich"]


@dataclass(frozen=True, slots=True)
class EnrichSettings:
    """The per-library enrichment toggle (slice of ``settings.enrich``)."""

    enabled: bool


@dataclass(frozen=True, slots=True)
class ProviderKey:
    """A provider's configured-ness, resolved from the secret store."""

    provider: str
    configured: bool


def should_enqueue_enrich(
    settings: EnrichSettings,
    providers: Iterable[ProviderKey],
) -> bool:
    """Return ``True`` iff an enrich job should be enqueued.

    Enrichment is gated on the library opt-in **and** at least one
    configured provider key. A provider with no key is skipped, so an
    all-unconfigured box yields ``False`` (the video still reaches
    ``READY`` — enrichment is simply not attempted).
    """
    if not settings.enabled:
        return False
    return any(p.configured for p in providers)
