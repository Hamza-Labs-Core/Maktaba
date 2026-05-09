"""Shared pytest plugin for Maktaba's test pyramid (Story 20.1).

The plugin offers three things, all opt-in via marker:

1. ``maktaba_testtier.softcap`` — pytest hooks that warn / fail on
   slow tests. Per-tier caps come from ``TIER_SOFT_CAPS`` and match
   AC4 of Story 20.1 (unit 100 ms, integration 5 s, e2e 30 s; >3x
   the cap fails the test).
2. ``maktaba_testtier.netguard`` — autouse fixture for unit-marked
   tests that monkey-patches ``socket.socket`` so any attempt to open
   a socket raises a clear, AC1-aligned error.
3. ``maktaba_testtier.tiers`` — the canonical tier names + per-tier
   wall-clock budgets, mirroring the Go ``testtier`` package.

The plugin is wired into pytest via ``pipeline/tests/conftest.py``;
plain ``import maktaba_testtier`` registers nothing on its own. The
indirection keeps the helper available to other future Python
packages in the repo without forcing a uv install.
"""

from .tiers import (
    E2E_TOTAL_BUDGET_S,
    INTEGRATION_TOTAL_BUDGET_S,
    PERF_CI_TOTAL_BUDGET_S,
    TIER_E2E,
    TIER_HARD_CAP_MULTIPLIER,
    TIER_INTEGRATION,
    TIER_PERF_CI,
    TIER_SOFT_CAPS,
    TIER_UNIT,
    UNIT_TOTAL_BUDGET_S,
)

__all__ = [
    "TIER_SOFT_CAPS",
    "TIER_HARD_CAP_MULTIPLIER",
    "TIER_UNIT",
    "TIER_INTEGRATION",
    "TIER_E2E",
    "TIER_PERF_CI",
    "UNIT_TOTAL_BUDGET_S",
    "INTEGRATION_TOTAL_BUDGET_S",
    "E2E_TOTAL_BUDGET_S",
    "PERF_CI_TOTAL_BUDGET_S",
]
