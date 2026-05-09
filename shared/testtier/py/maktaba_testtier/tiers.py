"""Tier names and budgets — single source of truth for Python tests.

These constants match the values exported by
``shared/testtier/go/tier.go``. Keep them in sync; the
``tools/test-budget`` enforcer reads both sides.
"""

from __future__ import annotations

TIER_UNIT = "unit"
TIER_INTEGRATION = "integration"
TIER_E2E = "e2e"
TIER_PERF_CI = "perf-ci"

# AC4: per-test soft caps, in seconds. A test that exceeds its tier's
# soft cap emits a warning; >3x the cap fails the test.
TIER_SOFT_CAPS: dict[str, float] = {
    TIER_UNIT: 0.1,
    TIER_INTEGRATION: 5.0,
    TIER_E2E: 30.0,
}

TIER_HARD_CAP_MULTIPLIER = 3

# Per-tier wall-clock budgets (the tools/test-budget enforcer reads
# these via the bash wrapper). Units: seconds.
UNIT_TOTAL_BUDGET_S = 60
INTEGRATION_TOTAL_BUDGET_S = 120
E2E_TOTAL_BUDGET_S = 300
PERF_CI_TOTAL_BUDGET_S = 120
