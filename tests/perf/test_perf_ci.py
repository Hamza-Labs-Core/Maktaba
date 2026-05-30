"""perf-ci gate: assert the CI-on-PR budget subset is real (Track V).

Before this track, ``make perf-ci`` was an ``echo`` stub that passed
unconditionally. These tests turn it into a real gate: the ci_pr
subset of ``shared/perf_budgets.yaml`` must exist, be non-empty, and
be well-formed. The "proves it asserts" check is that deleting the
budgets file makes this suite fail (see ci_subset.ci_pr_budgets
raising FileNotFoundError).

This is the reduced PR-time perf gate. The full perf regression suite
(actually measuring latencies against the budgets) runs nightly and
is out of scope for Track V.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from tests.perf.ci_subset import Budget, ci_pr_budgets

_VALID_SURFACES = {"rest", "graphql", "ws", "hls"}
_VALID_CACHE = {"warm", "cold"}


def test_ci_pr_subset_is_non_empty() -> None:
    """The PR perf gate must enforce at least one budget."""
    budgets = ci_pr_budgets()
    assert budgets, "expected at least one ci_pr=true endpoint budget"


def test_ci_pr_budgets_are_well_formed() -> None:
    """Every ci_pr budget has the fields the perf gate relies on."""
    budgets = ci_pr_budgets()
    for b in budgets:
        assert isinstance(b, Budget)
        assert b.id, "budget id must be non-empty"
        assert b.surface in _VALID_SURFACES, f"{b.id}: bad surface {b.surface!r}"
        assert b.path.startswith("/"), f"{b.id}: path must be absolute, got {b.path!r}"
        assert b.profile, f"{b.id}: profile must be non-empty"
        assert b.cache in _VALID_CACHE, f"{b.id}: bad cache {b.cache!r}"
        assert b.p95_ms > 0, f"{b.id}: p95_ms must be positive, got {b.p95_ms}"
        # Percentile ordering, when the optional fields are present.
        if b.p50_ms is not None:
            assert b.p50_ms <= b.p95_ms, f"{b.id}: p50 > p95"
        if b.p99_ms is not None:
            assert b.p99_ms >= b.p95_ms, f"{b.id}: p99 < p95"


def test_ci_pr_subset_excludes_non_ci_entries() -> None:
    """Sanity: the loader filters to ci_pr=true only.

    ``search_cold`` / ``hls_segment_cold`` / ``web_first_frame_warm``
    are ``ci_pr: false`` in the source file and must not leak into the
    PR subset.
    """
    ids = {b.id for b in ci_pr_budgets()}
    for excluded in ("search_cold", "hls_segment_cold", "web_first_frame_warm"):
        assert excluded not in ids, f"{excluded} is ci_pr=false but appeared in subset"


def test_missing_budgets_file_fails_loudly(tmp_path: Path) -> None:
    """Deleting/moving the budgets file must raise, not silently pass."""
    missing = tmp_path / "does-not-exist.yaml"
    with pytest.raises(FileNotFoundError):
        ci_pr_budgets(missing)
