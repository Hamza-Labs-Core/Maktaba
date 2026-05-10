"""Exponential-with-jitter retry backoff (Story 6.5).

Pure math, no I/O — separated from :mod:`maktaba_pipeline.db.jobs_state`
so it can be unit-tested without a database fake. The curve is fixed
by architecture §7.5 and the story's acceptance criteria:

    delay = min(base * 2 ** (attempts - 1), cap) * uniform(1 - jitter, 1 + jitter)

with the canonical defaults ``base=60``, ``cap=3600``, ``jitter=0.25``.
The jitter spreads the next retry across a 50% window so a fleet of
workers retrying the same transient outage stagger themselves rather
than dog-piling.

``attempts`` here is the counter AFTER the failed attempt (the value
the claim incremented). For the first failure ``attempts == 1`` →
~60 s; for the second ~120 s; capped at ~3600 s.
"""

from __future__ import annotations

import random

__all__ = [
    "BASE_SEC",
    "CAP_SEC",
    "JITTER_FRAC",
    "compute_backoff",
]


BASE_SEC: float = 60.0
CAP_SEC: float = 3600.0
JITTER_FRAC: float = 0.25


def compute_backoff(
    attempts: int,
    *,
    base: float = BASE_SEC,
    cap: float = CAP_SEC,
    jitter: float = JITTER_FRAC,
    rng: random.Random | None = None,
) -> float:
    """Backoff seconds for the given attempt count.

    Raises :class:`ValueError` for ``attempts < 1`` — the function is
    meaningless before the first failure.
    """
    if attempts < 1:
        raise ValueError("attempts must be >= 1")
    raw = min(base * (2 ** (attempts - 1)), cap)
    r = rng if rng is not None else random
    factor: float = 1.0 + (r.random() * 2 - 1) * jitter
    return float(raw * factor)
