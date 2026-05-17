"""Load the CI-on-PR performance budget subset (Track V, Story 20.7).

``shared/perf_budgets.yaml`` is the single source of truth for
performance budgets. PRs only enforce the fast subset — every
``endpoints`` entry with ``ci_pr: true``. This module parses that file
and exposes the subset as typed :class:`Budget` records so the
perf-ci gate can assert the subset is non-empty and well-formed.

The schema (see the header of ``shared/perf_budgets.yaml``):

    endpoints:
      <id>:
        surface: rest | graphql | ws | hls
        method:  GET | POST | ...        # absent for ws surfaces
        path:    "/api/..."
        profile: <hardware profile id>
        cache:   warm | cold
        p50_ms:  int                     # optional per entry
        p95_ms:  int                     # optional per entry
        p99_ms:  int                     # optional per entry
        ci_pr:   bool

``endpoints`` is a *mapping* keyed by id (not a list), and ``method`` /
the percentile fields are not present on every entry — only ``p95_ms``
is reliably set on the ci_pr subset, so it is the one required latency
field here.
"""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

import yaml

# Resolve shared/perf_budgets.yaml relative to the repo root (two
# parents up from this file: tests/perf/ci_subset.py -> repo root).
_REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_BUDGETS_PATH = _REPO_ROOT / "shared" / "perf_budgets.yaml"


@dataclass(frozen=True)
class Budget:
    """One CI-on-PR endpoint budget from ``perf_budgets.yaml``."""

    id: str
    surface: str
    path: str
    profile: str
    cache: str
    p95_ms: int
    method: str | None = None
    p50_ms: int | None = None
    p99_ms: int | None = None


def ci_pr_budgets(path: str | Path = DEFAULT_BUDGETS_PATH) -> list[Budget]:
    """Return the ``ci_pr: true`` endpoint budgets, sorted by id.

    Raises ``FileNotFoundError`` if the budgets file is missing (the
    perf-ci gate relies on this to fail loudly), and ``ValueError`` if
    the file is structurally unusable.
    """
    budgets_path = Path(path)
    if not budgets_path.is_file():
        raise FileNotFoundError(f"perf budgets file not found: {budgets_path}")

    raw = yaml.safe_load(budgets_path.read_text())
    if not isinstance(raw, dict):
        raise ValueError(f"{budgets_path}: top-level YAML is not a mapping")

    endpoints = raw.get("endpoints")
    if not isinstance(endpoints, dict):
        raise ValueError(f"{budgets_path}: 'endpoints' is missing or not a mapping")

    result: list[Budget] = []
    for endpoint_id, entry in endpoints.items():
        if not isinstance(entry, dict):
            raise ValueError(f"{budgets_path}: endpoint {endpoint_id!r} is not a mapping")
        if entry.get("ci_pr") is not True:
            continue

        try:
            p95_ms = int(entry["p95_ms"])
        except (KeyError, TypeError, ValueError) as exc:
            raise ValueError(
                f"{budgets_path}: ci_pr endpoint {endpoint_id!r} missing/invalid p95_ms"
            ) from exc

        result.append(
            Budget(
                id=str(endpoint_id),
                surface=str(entry["surface"]),
                path=str(entry["path"]),
                profile=str(entry["profile"]),
                cache=str(entry["cache"]),
                p95_ms=p95_ms,
                method=(str(entry["method"]) if "method" in entry else None),
                p50_ms=(int(entry["p50_ms"]) if "p50_ms" in entry else None),
                p99_ms=(int(entry["p99_ms"]) if "p99_ms" in entry else None),
            )
        )

    return sorted(result, key=lambda b: b.id)
