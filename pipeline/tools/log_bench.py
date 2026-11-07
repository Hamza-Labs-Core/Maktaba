"""The Python logger's cost per call (Epic 53 story 53.13, ``logger_overhead`` for Python).

    uv run --frozen python tools/log_bench.py [--calls N] [--out FILE]

The pipeline's logger (``maktaba_pipeline.log``: contextvars, redaction of keys and values,
the ring, the JSON renderer) logs ``--calls`` lines to ``/dev/null`` and each call is timed.
The budget is 20 µs at the 99th percentile on the reference machine (an Apple M2), times the
runner's speed against it: a fixed hashing and JSON loop, the factor clamped to 1–4, as the
Go benches' ``logbench.Calibrate`` does. Over budget, it exits 1 with the numbers; the numbers
are printed as JSON (and written to ``--out``) either way.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import logging
import os
import sys
import time

BUDGET_P99_NS = 20_000
# the calibration loop's time on the reference machine (an Apple M2, Python 3.12)
REFERENCE_CALIBRATION_S = 0.20


def calibrate() -> float:
    """The runner's speed against the reference: its loop's time over the reference's, 1–4."""
    best = float("inf")
    for _ in range(3):
        start = time.perf_counter()
        h = hashlib.sha256()
        for i in range(60_000):
            h.update(json.dumps({"i": i, "msg": "http_request", "path": "/api/videos"}).encode())
        best = min(best, time.perf_counter() - start)
    return min(4.0, max(1.0, best / REFERENCE_CALIBRATION_S))


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
    factor = calibrate()
    got = run(args.calls)
    limit = int(BUDGET_P99_NS * factor)
    report = {
        "logger_overhead_python": got,
        "_runner": {"factor": round(factor, 2), "budget_p99_ns": limit},
    }
    text = json.dumps(report, indent=2)
    print(text)  # noqa: T201
    if args.out:
        with open(args.out, "w") as f:
            f.write(text)
    if got["p99_ns"] > limit:
        print(  # noqa: T201
            f"a Python log call (p99): {got['p99_ns'] / 1000:.1f} µs, "
            f"over its budget of {limit / 1000:.1f} µs "
            f"(20 µs at the reference, runner factor {factor:.2f})",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
