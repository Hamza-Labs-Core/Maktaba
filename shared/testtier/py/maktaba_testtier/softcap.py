"""Soft-cap pytest hooks (AC4 of Story 20.1).

Wired in by ``pipeline/tests/conftest.py``::

    from maktaba_testtier.softcap import pytest_runtest_protocol  # noqa: F401

The hook reads the test's ``call`` phase duration and:

- ``dur <= cap``        → silent.
- ``cap < dur <= 3*cap`` → emits a ``UserWarning`` (visible in pytest
  output and in CI test reports). Won't fail the run.
- ``dur > 3*cap``       → the test runs once more as a whole (setup, call,
  teardown: fresh fixtures), alone in that moment: over 3*cap again, or failing
  then, → a test failure; under it → a warning naming the runner's load (a
  shared CI runner stalls for half a second now and then; the Go side's
  test-budget re-runs a slow test alone for the same reason).
"""

from __future__ import annotations

import sys
import warnings
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


def breached(first: float, again: float | None, hard_cap: float) -> bool:
    """A test breaks the hard cap only when its run again (when there was one) does
    too: one slow run with a fast one after it is the runner's load, not the test."""
    if first <= hard_cap:
        return False
    return again is None or again > hard_cap


def _call(reports: list[Any]) -> Any | None:
    return next((r for r in reports if r.when == "call"), None)


def _why_failed(reports: list[Any]) -> str:
    bad = next((r for r in reports if r.failed), None)
    if bad is None:
        return "it did not run"
    text = str(getattr(bad, "longreprtext", "") or bad.longrepr or "").strip()
    last = text.splitlines()[-1] if text else "failed"
    return f"{bad.when}: {last}"[:200]


@pytest.hookimpl(tryfirst=True)
def pytest_runtest_protocol(item: Any, nextitem: Any) -> bool | None:
    """Run a tiered test, judge its ``call`` time, and only then log its reports.

    Over the hard cap (and passing), the test runs once more as a whole: setup, call and
    teardown again, so every function-scoped fixture (``tmp_path`` and all that depends
    on it) is made afresh, as for a first run. Running only the call again reused the
    first run's fixtures: a test met its own files (``mkdir`` raised, an append found
    lines already there) and one stall of the runner failed it. pytest-rerunfailures
    runs a test again the same way (``runtestprotocol``)."""
    cap = _cap_for_item(item)
    if cap is None:
        return None  # pytest's own protocol
    from _pytest.runner import runtestprotocol

    ihook = item.ihook
    ihook.pytest_runtest_logstart(nodeid=item.nodeid, location=item.location)
    reports = runtestprotocol(item, nextitem=nextitem, log=False)
    call = _call(reports)
    if call is not None and call.passed:
        _judge(item, call, cap, nextitem)
    for rep in reports:
        ihook.pytest_runtest_logreport(report=rep)
    ihook.pytest_runtest_logfinish(nodeid=item.nodeid, location=item.location)
    return True


def _judge(item: Any, call: Any, cap: float, nextitem: Any) -> None:
    from _pytest.runner import runtestprotocol

    first = float(call.duration)
    hard_cap = cap * TIER_HARD_CAP_MULTIPLIER
    if first <= hard_cap:
        if first > cap:
            warnings.warn(
                f"{item.nodeid}: took {first:.3f}s > soft cap {cap:.3f}s (Story 20.1 AC4)",
                SoftCapWarning,
                stacklevel=2,
            )
        return
    again_reports = runtestprotocol(item, nextitem=nextitem, log=False)
    again_call = _call(again_reports)
    ok = again_call is not None and all(not r.failed for r in again_reports)
    again = float(again_call.duration) if ok and again_call is not None else None
    if not breached(first, again, hard_cap):
        warnings.warn(
            f"{item.nodeid}: took {first:.3f}s > {TIER_HARD_CAP_MULTIPLIER}x soft cap "
            f"{cap:.3f}s, {again:.3f}s run again: the runner's load, not the test",
            SoftCapWarning,
            stacklevel=2,
        )
        return
    call.outcome = "failed"
    call.longrepr = (
        f"AssertionError: test took {first:.3f}s > {TIER_HARD_CAP_MULTIPLIER}x "
        f"soft cap {cap:.3f}s (Story 20.1 AC4)"
        + (
            f", and {again:.3f}s run again"
            if again is not None
            else f", and the run again failed ({_why_failed(again_reports)})"
        )
    )
