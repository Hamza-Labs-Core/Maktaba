"""YAML loader for ``shared/perf_budgets.yaml``.

Mirrors :mod:`api/internal/perf` so the pipeline can read the same
budgets without duplicating ground truth.
"""

from __future__ import annotations

import dataclasses
import pathlib
from typing import Any

try:
    import yaml
except ImportError:  # pragma: no cover
    yaml = None  # type: ignore[assignment]


@dataclasses.dataclass(frozen=True)
class BudgetEntry:
    """One endpoint budget."""

    surface: str
    path: str
    profile: str
    cache: str
    p50_ms: int | None
    p95_ms: int
    p99_ms: int | None
    ci_pr: bool


@dataclasses.dataclass(frozen=True)
class Throughput:
    profile: str
    target: int


@dataclasses.dataclass(frozen=True)
class Budgets:
    """Parsed root."""

    version: int
    endpoints: dict[str, BudgetEntry]
    throughputs: dict[str, Throughput]


def load_budgets(path: str | pathlib.Path) -> Budgets:
    """Load + validate the YAML at ``path``.

    Raises ``ValueError`` on schema problems (missing keys, p99 < p95).
    """
    if yaml is None:  # pragma: no cover
        raise RuntimeError("pyyaml is not installed")

    raw: Any = yaml.safe_load(pathlib.Path(path).read_text())
    if not isinstance(raw, dict):
        raise ValueError("budgets file must be a mapping")
    version = int(raw.get("version", 0))
    if version <= 0:
        raise ValueError("budgets: version required")
    eps_raw: dict[str, Any] = raw.get("endpoints", {}) or {}
    endpoints: dict[str, BudgetEntry] = {}
    for eid, e in eps_raw.items():
        if not isinstance(e, dict):
            raise ValueError(f"endpoint {eid}: not a mapping")
        p95 = e.get("p95_ms")
        if p95 is None:
            raise ValueError(f"endpoint {eid}: p95_ms required")
        p99 = e.get("p99_ms")
        if p99 is not None and p99 < p95:
            raise ValueError(f"endpoint {eid}: p99 < p95")
        p50 = e.get("p50_ms")
        if p50 is not None and p50 > p95:
            raise ValueError(f"endpoint {eid}: p50 > p95")
        endpoints[eid] = BudgetEntry(
            surface=str(e.get("surface", "")),
            path=str(e.get("path", "")),
            profile=str(e.get("profile", "")),
            cache=str(e.get("cache", "")),
            p50_ms=int(p50) if p50 is not None else None,
            p95_ms=int(p95),
            p99_ms=int(p99) if p99 is not None else None,
            ci_pr=bool(e.get("ci_pr", False)),
        )
    tps_raw: dict[str, Any] = raw.get("throughputs", {}) or {}
    throughputs = {
        tid: Throughput(profile=str(t["profile"]), target=int(t["target"]))
        for tid, t in tps_raw.items()
    }
    return Budgets(version=version, endpoints=endpoints, throughputs=throughputs)
