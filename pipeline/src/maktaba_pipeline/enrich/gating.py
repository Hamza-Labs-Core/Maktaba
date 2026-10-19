"""Enrich-enqueue gating (Story 26.7 D4).

On ``classify`` completion the pipeline fire-and-forgets an enrich job —
but only when enrichment is actually possible. Two conditions must hold
(Story 26.7 AC):

1. the library opted in (``settings.enrich.enabled``; ``rules.enrich.auto``); and
2. at least one provider is configured. Maktaba's own intel
   (:data:`INTERNAL_PROVIDER`) always is: it needs no key and no network, so a
   keyless box still gets an internal suggestion; the web search (Google, Ollama)
   and the language model add to it when configured.

The check is a pure function over small value objects so the wiring
(:mod:`maktaba_pipeline.enrich.worker` / the classify stage) and its
tests never need a live settings store or secret backend.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass

__all__ = ["INTERNAL_PROVIDER", "EnrichSettings", "ProviderKey", "should_enqueue_enrich"]


@dataclass(frozen=True, slots=True)
class EnrichSettings:
    """The per-library enrichment toggle (slice of ``settings.enrich``)."""

    enabled: bool


@dataclass(frozen=True, slots=True)
class ProviderKey:
    """A provider's configured-ness, resolved from the secret store."""

    provider: str
    configured: bool


#: Maktaba's own intel: always available (stub audit #4: no provider ever was)
INTERNAL_PROVIDER = ProviderKey("internal", True)


def should_enqueue_enrich(
    settings: EnrichSettings,
    providers: Iterable[ProviderKey],
) -> bool:
    """Return ``True`` iff an enrich job should be enqueued.

    Enrichment is gated on the library opt-in **and** at least one
    configured provider. A provider with no key is skipped; pass
    :data:`INTERNAL_PROVIDER` (the callers do) and an opted-in library is
    always enriched from its own intel.
    """
    if not settings.enabled:
        return False
    return any(p.configured for p in providers)
