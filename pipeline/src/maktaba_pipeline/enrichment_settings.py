"""The ``enrichment`` rules (Epic 26, story 26.6): what Maktaba does with a metadata match
waiting in the review queue. The pipeline never reads them; the API's auto-accept sweep
(``api/internal/handlers/enrichment/autoaccept.go``) does. They live here because every
rules section's schema (``api/internal/handlers/rules/schema.json``) is generated from one
place.

``auto_accept_threshold`` defaults to 0 (off): every match waits for a person, as before."""

from __future__ import annotations

from dataclasses import dataclass

from .settings import Help, register

__all__ = ["EnrichmentConfig"]

ENR = "Metadata review"


@dataclass(frozen=True)
class EnrichmentConfig:
    """The metadata review queue (rules ``enrichment``)."""

    #: a waiting match at or above this confidence (0-1) is accepted by itself; 0: off
    auto_accept_threshold: float = 0.0


register(
    EnrichmentConfig,
    {
        "auto_accept_threshold": Help(
            ENR,
            "Accept matches by themselves from (confidence)",
            "A match waiting in the review queue whose confidence is at least this (0 to 1, "
            "0.9 is 90%) is accepted without a person, checked every minute. Fields you "
            "edited yourself are never replaced, and an accepted match can be reverted to "
            "the original. 0: off, every match waits for review.",
            minimum=0,
            maximum=1,
        ),
    },
)
