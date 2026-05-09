"""Soft-cap pytest hooks (AC4 of Story 20.1).

Wired in by ``pipeline/tests/conftest.py``::

    from maktaba_testtier.softcap import (
        pytest_runtest_logreport,  # noqa: F401
    )

The hook reads the test's ``call`` phase duration and:

- ``dur <= cap``        → silent.
- ``cap < dur <= 3*cap`` → emits a ``UserWarning`` (visible in pytest
  output and in CI test reports). Won't fail the run.
- ``dur > 3*cap``       → re-raises as a test failure via the
  ``pytest_runtest_makereport`` hook by mutating the report.
"""

from __future__ import annotations

import warnings
from typing import Any

from .tiers import TIER_HARD_CAP_MULTIPLIER, TIER_SOFT_CAPS


def _cap_for_item(item: Any) -> float | None:
    for tier, cap in TIER_SOFT_CAPS.items():
        if item.get_closest_marker(tier) is not None:
            return cap
    return None


class SoftCapWarning(UserWarning):
    """Emitted when a test exceeds its tier's per-test soft cap."""


def pytest_runtest_makereport(item: Any, call: Any) -> Any | None:
    """Failure-path hook: turn a >3× breach into a failed report.

    pytest's plugin protocol allows this hook to return a custom
    ``TestReport``. We only intervene on the ``call`` phase and only
    when the test was otherwise passing — a test that already failed
    keeps its original failure as the headline.
    """
    if call.when != "call":
        return None
    cap = _cap_for_item(item)
    if cap is None:
        return None
    duration = getattr(call, "duration", None)
    if duration is None:
        return None

    hard_cap = cap * TIER_HARD_CAP_MULTIPLIER
    if duration > hard_cap and call.excinfo is None:
        # Mutate the report path: raise a synthetic AssertionError via
        # outcome so the standard pytest failure flow takes over.
        from _pytest.runner import CallInfo

        msg = (
            f"test took {duration:.3f}s > {TIER_HARD_CAP_MULTIPLIER}x "
            f"soft cap {cap:.3f}s (Story 20.1 AC4)"
        )

        def _raise() -> None:
            raise AssertionError(msg)

        # Replace call with a failing CallInfo so the surrounding
        # report machinery generates a proper failure entry.
        new_call = CallInfo.from_call(_raise, when="call")
        call.excinfo = new_call.excinfo
        return None

    if duration > cap:
        warnings.warn(
            f"{item.nodeid}: took {duration:.3f}s > soft cap {cap:.3f}s (Story 20.1 AC4)",
            SoftCapWarning,
            stacklevel=2,
        )
    return None
