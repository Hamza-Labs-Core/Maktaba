#!/usr/bin/env python3
"""Compare two runs of the log benches (Epic 53 story 53.13 §2): a cost that grew by more than
``--over`` (20 % by default) since the last run is a regression.

    compare.py PREVIOUS.json CURRENT.json [--over 0.2] [--markdown OUT.md]

Each file is what the Go benches print (``LOGBENCH_OUT``) merged with the Python bench's
(``LOGBENCH_PY_OUT``): ``{bench: {metric: number}}``. A metric is a cost when its name ends in
``_ns``, ``_us``, ``_ms``, ``_ns_per_line``, ``_us_per_line`` or ``_bytes`` (``worst_bytes``);
counts (``lines``, ``calls``) and the runner's own block (``_runner``) are not compared. Costs
are compared after dividing by each run's runner factor, so a slower runner is not a
regression. Exit 1 when one regressed; the Markdown report (``--markdown``) is the issue's body.
Standard library only.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

COST_SUFFIXES = ("_ns", "_us", "_ms", "_per_line", "worst_bytes")


def is_cost(metric: str) -> bool:
    return metric.endswith(COST_SUFFIXES) and metric != "cap_bytes"


def factor(run: dict[str, Any]) -> float:
    try:
        return max(1.0, float(run.get("_runner", {}).get("factor", 1.0)))
    except (TypeError, ValueError):
        return 1.0


def regressions(
    prev: dict[str, Any], cur: dict[str, Any], over: float = 0.2
) -> list[tuple[str, str, float, float, float]]:
    """(bench, metric, previous, current, change) for each cost that grew by more than ``over``,
    each normalised by its run's runner factor."""
    fp, fc = factor(prev), factor(cur)
    out = []
    for bench, metrics in sorted(cur.items()):
        if bench.startswith("_") or not isinstance(metrics, dict):
            continue
        before = prev.get(bench)
        if not isinstance(before, dict):
            continue
        for metric, value in sorted(metrics.items()):
            old = before.get(metric)
            if (
                not is_cost(metric)
                or not isinstance(value, (int, float))
                or not isinstance(old, (int, float))
                or old <= 0
            ):
                continue
            change = (value / fc) / (old / fp) - 1
            if change > over:
                out.append((bench, metric, float(old), float(value), change))
    return out


def markdown(found: list[tuple[str, str, float, float, float]], over: float) -> str:
    lines = [
        "The nightly log benches (Epic 53 story 53.13) found costs that grew by more than "
        f"{over:.0%} since the last run:",
        "",
        "| Bench | Metric | Last run | This run | Change |",
        "|---|---|---|---|---|",
    ]
    lines += [f"| {b} | `{m}` | {o:g} | {v:g} | +{c:.0%} |" for b, m, o, v, c in found]
    lines += [
        "",
        "Runner factors are taken out before comparing. "
        "The run's numbers are in its `logbench-nightly` artifact.",
    ]
    return "\n".join(lines) + "\n"


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("previous")
    ap.add_argument("current")
    ap.add_argument("--over", type=float, default=0.2)
    ap.add_argument("--markdown")
    a = ap.parse_args(argv)
    prev = json.loads(Path(a.previous).read_text())
    cur = json.loads(Path(a.current).read_text())
    found = regressions(prev, cur, a.over)
    for b, m, o, v, c in found:
        print(f"{b}.{m}: {o:g} -> {v:g} (+{c:.0%})")  # noqa: T201
    if a.markdown:
        Path(a.markdown).write_text(markdown(found, a.over) if found else "")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main())
