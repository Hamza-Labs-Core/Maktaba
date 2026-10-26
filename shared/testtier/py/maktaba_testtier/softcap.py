"""Soft-cap pytest hooks (AC4 of Story 20.1).

Wired in by ``pipeline/tests/conftest.py``::

    from maktaba_testtier.softcap import (
        pytest_runtest_logreport,  # noqa: F401
    )

The hook reads the test's ``call`` phase duration and:

- ``dur <= cap``        → silent.
- ``cap < dur <= 3*cap`` → emits a ``UserWarning`` (visible in pytest
  output and in CI test reports). Won't fail the run.
- ``dur > 3*cap``       → the test runs once more, alone in that moment
  (``pytest_runtest_call``): over 3*cap again → a test failure via the
  ``pytest_runtest_makereport`` hook; under it → a warning naming the runner's
  load (a shared CI runner stalls for half a second now and then; the Go side's
  test-budget re-runs a slow test alone for the same reason).
"""

from __future__ import annotations

import sys
import time
import warnings
from collections.abc import Generator
from typing import Any

import pytest

from .tiers import (
    COVERAGE_SOFT_CAP_SCALE,
    TIER_HARD_CAP_MULTIPLIER,
    TIER_SOFT_CAPS,
    WINDOWS_SOFT_CAP_SCALE,
)


def _platform_scale(platform: str = sys.platform) -> float:
    return WINDOWS_SOFT_CAP_SCALE if platform == "win32" else 1.0


def _coverage_scale(measuring: bool | None = None) -> float:
    if measuring is None:
        measuring = "coverage" in sys.modules
    return COVERAGE_SOFT_CAP_SCALE if measuring else 1.0


def _cap_for_item(item: Any) -> float | None:
    for tier, cap in TIER_SOFT_CAPS.items():
        if item.get_closest_marker(tier) is not None:
            return cap * _platform_scale() * _coverage_scale()
    return None


class SoftCapWarning(UserWarning):
    """Emitted when a test exceeds its tier's per-test soft cap."""


#: the first run's duration, and the second's when the first broke the hard cap
_FIRST = pytest.StashKey[float]()
_AGAIN = pytest.StashKey["float | None"]()


def breached(first: float, again: float | None, hard_cap: float) -> bool:
    """A test breaks the hard cap only when its run again (when there was one) does
    too: one slow run with a fast one after it is the runner's load, not the test."""
    if first <= hard_cap:
        return False
    return again is None or again > hard_cap


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_call(item: Any) -> Generator[None, Any, None]:
    """Time the test; over the hard cap (and passing), run it once more."""
    began = time.perf_counter()
    outcome = yield
    first = time.perf_counter() - began
    item.stash[_FIRST] = first
    cap = _cap_for_item(item)
    if cap is None or outcome.excinfo is not None or first <= cap * TIER_HARD_CAP_MULTIPLIER:
        return
    began = time.perf_counter()
    try:
        item.runtest()
    except Exception:  # noqa: BLE001 — the second run's failure leaves the first verdict
        item.stash[_AGAIN] = None
        return
    item.stash[_AGAIN] = time.perf_counter() - began


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
    # the first run's own time (call.duration counts a second run too)
    duration = item.stash.get(_FIRST, getattr(call, "duration", None))
    if duration is None:
        return None

    hard_cap = cap * TIER_HARD_CAP_MULTIPLIER
    again = item.stash.get(_AGAIN, None)
    if duration > hard_cap and call.excinfo is None and not breached(duration, again, hard_cap):
        warnings.warn(
            f"{item.nodeid}: took {duration:.3f}s > {TIER_HARD_CAP_MULTIPLIER}x soft cap "
            f"{cap:.3f}s, {again:.3f}s run again: the runner's load, not the test",
            SoftCapWarning,
            stacklevel=2,
        )
        return None
    if duration > hard_cap and call.excinfo is None:
        # Mutate the report path: raise a synthetic AssertionError via
        # outcome so the standard pytest failure flow takes over.
        from _pytest.runner import CallInfo

        msg = (
            f"test took {duration:.3f}s > {TIER_HARD_CAP_MULTIPLIER}x "
            f"soft cap {cap:.3f}s (Story 20.1 AC4)"
            + (f", and {again:.3f}s run again" if again is not None else "")
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
