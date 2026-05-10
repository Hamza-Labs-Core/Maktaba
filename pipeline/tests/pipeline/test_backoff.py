"""Story 6.5 — backoff curve and jitter properties.

Pure-Python math, no DB. Pinned to the architecture §7.5 curve:
``min(60 * 2^(attempts-1), 3600) ± 25%``.
"""

from __future__ import annotations

import random
import statistics

import pytest

from maktaba_pipeline.pipeline.backoff import (
    BASE_SEC,
    CAP_SEC,
    JITTER_FRAC,
    compute_backoff,
)


def test_backoff_constants_match_arch_7_5() -> None:
    """Curve constants are fixed by the architecture; changing them
    breaks the published behaviour."""
    assert BASE_SEC == 60.0
    assert CAP_SEC == 3600.0
    assert JITTER_FRAC == 0.25


def test_backoff_first_attempt_is_about_60s() -> None:
    rng = random.Random(0)
    val = compute_backoff(1, rng=rng)
    assert 45.0 <= val <= 75.0


def test_backoff_doubles_until_cap() -> None:
    """1st ~60, 2nd ~120, 3rd ~240, ..., capped at ~3600."""
    rng = random.Random(0)
    raws = [60, 120, 240, 480, 960, 1920, 3600, 3600, 3600]
    for i, raw in enumerate(raws, start=1):
        val = compute_backoff(i, rng=rng)
        lo = raw * (1 - JITTER_FRAC)
        hi = raw * (1 + JITTER_FRAC)
        assert lo <= val <= hi, f"attempt={i} val={val} not in [{lo}, {hi}]"


def test_backoff_jitter_within_25pct() -> None:
    """1000 samples at attempt=3 stay within the ±25% band and average
    close to the raw value."""
    rng = random.Random(1234)
    raw = 240.0
    samples = [compute_backoff(3, rng=rng) for _ in range(1000)]
    assert min(samples) >= raw * (1 - JITTER_FRAC)
    assert max(samples) <= raw * (1 + JITTER_FRAC)
    mean = statistics.mean(samples)
    # Mean of uniform(0.75, 1.25) is 1.0; expect mean ≈ raw within 1%.
    assert abs(mean - raw) / raw < 0.02


def test_backoff_deterministic_with_seeded_rng() -> None:
    """Same seed → same value (test reproducibility)."""
    a = compute_backoff(5, rng=random.Random(42))
    b = compute_backoff(5, rng=random.Random(42))
    assert a == b


def test_backoff_invalid_attempts_raises() -> None:
    with pytest.raises(ValueError, match="attempts"):
        compute_backoff(0)
    with pytest.raises(ValueError, match="attempts"):
        compute_backoff(-1)


def test_backoff_cap_respected_far_past_cap() -> None:
    """Even at attempts=20 the value stays within the capped band."""
    rng = random.Random(999)
    val = compute_backoff(20, rng=rng)
    assert CAP_SEC * (1 - JITTER_FRAC) <= val <= CAP_SEC * (1 + JITTER_FRAC)


def test_backoff_uses_module_random_when_no_rng() -> None:
    """Default rng path returns a value in the right band."""
    val = compute_backoff(2)
    raw = 120.0
    assert raw * (1 - JITTER_FRAC) <= val <= raw * (1 + JITTER_FRAC)


def test_backoff_custom_base_and_cap() -> None:
    """Knobs are wired through; tests can pin a tight curve."""
    val = compute_backoff(1, base=10.0, cap=100.0, jitter=0.0)
    assert val == 10.0
    val = compute_backoff(10, base=10.0, cap=100.0, jitter=0.0)
    assert val == 100.0
