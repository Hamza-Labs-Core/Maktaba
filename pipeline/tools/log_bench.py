"""The Python logger's cost per call (Epic 53 story 53.13, ``logger_overhead`` for Python).

    uv run --frozen python tools/log_bench.py [--calls N] [--out FILE]

The pipeline's logger (``maktaba_pipeline.log``: contextvars, redaction of keys and values,
the ring, the JSON renderer) logs ``--calls`` lines to ``/dev/null`` and each call is timed.
The budget is 20 µs at the 99th percentile on the reference machine (an Apple M2), times the
runner's speed against it: the slower of a hashing and JSON loop and a loop of the logger's
own work (the report shows both times), clamped to 1–4, as the Go benches'
``logbench.Calibrate`` does. Over budget it warns (a GitHub
``::warning::`` with the numbers) and exits 1 only with ``LOGBENCH_PY_STRICT=1``: the
story's rollout starts a budget as a warning, and the Python logger is over it today (see
docs/performance/logs.md). The numbers are printed as JSON (and written to ``--out``) either
way, and the nightly run compares them night to night.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import logging
import os
import sys
import time
from collections.abc import Callable

BUDGET_P99_NS = 20_000
# the calibration loops' times on the reference machine, measured as Go's logbench.Calibrate's
# references were (the same Mac meets those within 4 %): a hashing and JSON loop, and the
# logger's own kind of work (scrub a line's values with the shared rules, encode it). Until
# issue #744 the first was 0.20 s, ~3.6 times the reference's: every runner up to that slow
# came out at factor 1, CI's (2.8 times slower at logging) among them.
REFERENCE_HASH_S = 0.055
REFERENCE_LINES_S = 0.060


def _best_of_three(work: Callable[[], None]) -> float:
    best = float("inf")
    for _ in range(3):
        start = time.perf_counter()
        work()
        best = min(best, time.perf_counter() - start)
    return best


def _hash_work() -> None:
    h = hashlib.sha256()
    for i in range(60_000):
        h.update(json.dumps({"i": i, "msg": "http_request", "path": "/api/videos"}).encode())


def _lines_work() -> None:
    from maktaba_pipeline.log import redact

    values = ("http_request", "/api/videos/123?token=abc", "segment 12 of 40 from host-2", "ok")
    for i in range(10_000):
        line = {"ts": "2026-11-07T10:00:00.000001Z", "level": "info", "status": 200, "i": i}
        for n, v in enumerate(values):
            line[f"f{n}"] = redact.scrub(v)
        json.dumps(line)


def calibrate() -> tuple[float, dict[str, float]]:
    """The runner's speed against the reference (the slower of the two loops, 1–4) and the
    loops' times, which the report shows."""
    raw = {"hash_s": _best_of_three(_hash_work), "lines_s": _best_of_three(_lines_work)}
    f = max(raw["hash_s"] / REFERENCE_HASH_S, raw["lines_s"] / REFERENCE_LINES_S)
    return min(4.0, max(1.0, f)), {k: round(v, 4) for k, v in raw.items()}


def percentile(sorted_ns: list[int], q: float) -> int:
    return sorted_ns[min(len(sorted_ns) - 1, int(q * len(sorted_ns)))]


def run(calls: int) -> dict[str, float | int]:
    os.environ["MAKTABA_LOG_FORMAT"] = "json"
    import structlog

    from maktaba_pipeline import log as mlog

    mlog.init(service="bench", env="prod", version="bench", level=logging.INFO)
    devnull = open(os.devnull, "w")  # noqa: SIM115 - kept open for the run
    structlog.configure(logger_factory=structlog.PrintLoggerFactory(file=devnull))
    logger = mlog.get_logger()
    # warm up the caches and the first-call paths
    for _ in range(2_000):
        logger.info("http_request", path="/api/videos/123?token=abc", status=200, took_ms=1.2)
    times = [0] * calls
    clock = time.perf_counter_ns
    for i in range(calls):
        s = clock()
        logger.info(
            "http_request", path="/api/videos/123?token=abc", status=200, user_id="u-1", took_ms=1.2
        )
        times[i] = clock() - s
    devnull.close()
    times.sort()
    return {"calls": calls, "p50_ns": percentile(times, 0.5), "p99_ns": percentile(times, 0.99)}


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument(
        "--calls", type=int, default=int(200_000 * float(os.environ.get("LOGBENCH_SCALE", "1")))
    )
    ap.add_argument("--out", default=os.environ.get("LOGBENCH_PY_OUT", ""))
    args = ap.parse_args(argv)
    factor, raw = calibrate()
    got = run(args.calls)
    limit = int(BUDGET_P99_NS * factor)
    report = {
        "logger_overhead_python": got,
        "_runner": {"factor": round(factor, 2), "budget_p99_ns": limit, "calibration": raw},
    }
    text = json.dumps(report, indent=2)
    print(text)  # noqa: T201
    if args.out:
        with open(args.out, "w") as f:
            f.write(text)
    if got["p99_ns"] > limit:
        why = (
            f"a Python log call (p99): {got['p99_ns'] / 1000:.1f} µs, "
            f"over its budget of {limit / 1000:.1f} µs "
            f"(20 µs at the reference, runner factor {factor:.2f})"
        )
        strict = os.environ.get("LOGBENCH_PY_STRICT", "") == "1"
        print(f"::{'error' if strict else 'warning'}::{why}", file=sys.stderr)  # noqa: T201
        return 1 if strict else 0
    return 0


if __name__ == "__main__":
    sys.exit(main())
