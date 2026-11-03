#!/usr/bin/env python3
"""The Core bundle's size budget (Epic 51 story 51.17, N7-N9).

The baseline is the Full Mac dmg of v0.3.62 (baseline.json). Core must stay at or under
45% of it and under 400 MB (N7). When the caller measured a running Core home it also
passes its idle memory (N8, at or under 700 MB) and its process count besides the
launcher and the supervisor (N9, exactly 4), or --home, the JSON core-home.sh wrote.

    bundle_size.py --core-bytes N [--full-bytes N] [--idle-rss-mb N] [--processes N] [--home FILE]

Exits 1 when a budget is broken, and prints one line per check either way (a markdown
table with --summary, for the job summary).
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass
from pathlib import Path

BASELINE = Path(__file__).with_name("baseline.json")
MB = 1_000_000


@dataclass(frozen=True)
class Check:
    name: str
    value: float
    limit: float
    unit: str
    # exact: the value must be the limit (the process count), not only at or under it
    exact: bool = False

    @property
    def ok(self) -> bool:
        return self.value == self.limit if self.exact else self.value <= self.limit


def load_baseline(path: Path = BASELINE) -> dict:
    with path.open(encoding="utf-8") as f:
        doc = json.load(f)
    for key in ("full_dmg_bytes", "core_share", "core_max_bytes", "idle_rss_mb", "processes"):
        if key not in doc:
            raise ValueError(f"baseline.json has no {key}")
    return doc


def checks(base: dict, core_bytes: int, full_bytes: int | None = None,
           idle_rss_mb: float | None = None, processes: int | None = None) -> list[Check]:
    """The budgets the measures given are held to; a measure not given is not checked."""
    if core_bytes <= 0:
        raise ValueError("the core size must be above 0")
    full = full_bytes if full_bytes else base["full_dmg_bytes"]
    out = [
        Check("core / full", core_bytes / full, base["core_share"], "share"),
        Check("core size", core_bytes / MB, base["core_max_bytes"] / MB, "MB"),
    ]
    if idle_rss_mb is not None:
        out.append(Check("idle memory", idle_rss_mb, base["idle_rss_mb"], "MB"))
    if processes is not None:
        out.append(Check("processes", processes, base["processes"], "processes", exact=True))
    return out


def render(cs: list[Check], summary: bool) -> str:
    def fmt(v: float, unit: str) -> str:
        return f"{v:.1%}" if unit == "share" else f"{v:.1f} {unit}"

    if summary:
        rows = ["| check | value | budget | |", "|---|---|---|---|"]
        rows += [f"| {c.name} | {fmt(c.value, c.unit)} | {'exactly ' if c.exact else ''}{fmt(c.limit, c.unit)} | "
                 f"{'ok' if c.ok else 'OVER'} |" for c in cs]
        return "\n".join(rows)
    return "\n".join(f"{'ok  ' if c.ok else 'FAIL'} {c.name}: {fmt(c.value, c.unit)} "
                     f"(budget {'exactly ' if c.exact else ''}{fmt(c.limit, c.unit)})" for c in cs)


def load_home(path: Path) -> tuple[float, int]:
    """The idle memory and the process count core-home.sh measured."""
    with path.open(encoding="utf-8") as f:
        doc = json.load(f)
    return float(doc["idle_rss_mb"]), int(doc["processes"])


def main(argv: list[str]) -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--core-bytes", type=int, required=True)
    p.add_argument("--full-bytes", type=int)
    p.add_argument("--idle-rss-mb", type=float)
    p.add_argument("--processes", type=int)
    p.add_argument("--home", type=Path, help="core-home.sh's JSON: its idle memory and process count")
    p.add_argument("--baseline", type=Path, default=BASELINE)
    p.add_argument("--summary", action="store_true")
    a = p.parse_args(argv)
    if a.home is not None:
        rss, procs = load_home(a.home)
        a.idle_rss_mb = rss if a.idle_rss_mb is None else a.idle_rss_mb
        a.processes = procs if a.processes is None else a.processes
    cs = checks(load_baseline(a.baseline), a.core_bytes, a.full_bytes, a.idle_rss_mb, a.processes)
    print(render(cs, a.summary))
    return 0 if all(c.ok for c in cs) else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
