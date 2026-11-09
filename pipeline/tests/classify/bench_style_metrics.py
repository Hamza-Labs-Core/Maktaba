"""Benchmark of ``production_style.compute_metrics``: the shipped version against the
reference kept in ``style_reference`` (the version before the speed-up), on the same
inputs as the regression test.

Run from ``pipeline/``::

    uv run --extra dev python -m tests.classify.bench_style_metrics [--runs 7]

For each input set and version it prints the median over the runs of the wall time and the
CPU time of one pass over every video, and the peak memory traced in one more pass
(tracemalloc, apart from the timed runs: tracing slows the code it traces).
"""

from __future__ import annotations

import argparse
import gc
import platform
import statistics
import sys
import time
import tracemalloc
from collections.abc import Callable, Sequence
from typing import Any

from maktaba_pipeline.classify import production_style
from tests.classify import style_reference
from tests.classify.style_inputs import Case, parity_cases, season_cases

Compute = Callable[..., Any]


def say(line: str) -> None:
    sys.stdout.write(line + "\n")


def one_pass(compute: Compute, cases: Sequence[Case]) -> None:
    for _name, windows, info in cases:
        compute(windows, info)


def measure(compute: Compute, cases: Sequence[Case], runs: int) -> dict[str, float]:
    walls, cpus = [], []
    one_pass(compute, cases)  # warm: imports, caches of the inputs
    for _ in range(runs):
        gc.collect()
        w0, c0 = time.perf_counter(), time.process_time()
        one_pass(compute, cases)
        walls.append(time.perf_counter() - w0)
        cpus.append(time.process_time() - c0)
    gc.collect()
    tracemalloc.start()
    one_pass(compute, cases)
    _, peak = tracemalloc.get_traced_memory()
    tracemalloc.stop()
    return {
        "wall_s": statistics.median(walls),
        "cpu_s": statistics.median(cpus),
        "peak_kib": peak / 1024,
    }


def main(argv: Sequence[str] | None = None) -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--runs", type=int, default=7)
    args = ap.parse_args(argv)
    sets = {"parity worlds": parity_cases(), "season of 30": season_cases()}
    versions: dict[str, Compute] = {
        "before": style_reference.compute_metrics,
        "after": production_style.compute_metrics,
    }
    say(f"{platform.machine()} {platform.processor()} python {platform.python_version()}")
    say(f"median of {args.runs} runs, one pass = compute_metrics on every video of the set")
    say(f"{'inputs':<16}{'videos':>7}{'version':>9}{'wall s':>9}{'cpu s':>9}{'peak KiB':>10}")
    for label, cases in sets.items():
        for version, compute in versions.items():
            r = measure(compute, cases, args.runs)
            say(
                f"{label:<16}{len(cases):>7}{version:>9}"
                f"{r['wall_s']:>9.3f}{r['cpu_s']:>9.3f}{r['peak_kib']:>10.0f}"
            )


if __name__ == "__main__":
    main()
