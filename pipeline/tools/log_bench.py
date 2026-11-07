"""The Python logger's cost per call (Epic 53 story 53.13, ``logger_overhead`` for Python).

    uv run --frozen python tools/log_bench.py [--calls N] [--out FILE]

The pipeline's logger (``maktaba_pipeline.log``: contextvars, redaction of keys and values,
the ring, the JSON renderer) logs ``--calls`` lines to ``/dev/null`` and each call is timed.
The budget is 20 µs at the 99th percentile on the reference machine (an Apple M2), times the
runner's speed against it: the slowest of a hashing and JSON loop, a loop of the logger's
own work, that work's p99 in small chunks, and a call's p99 through structlog's stock
pipeline (the report shows each), clamped to 1–4, as the Go benches'
``logbench.Calibrate`` does. With ``--base-src`` (``LOGBENCH_PY_BASE_SRC``: the
``pipeline/src`` of the commit a change goes onto) it also measures that commit's logger on
the same runner, round for round, and over the budget fails a change only when it is also
more than 15 % slower than that: the factor follows the logger only within ~15 % across CI's
CPUs (issue #807). Over budget it warns (a GitHub
``::warning::`` with the numbers) and exits 1 only with ``LOGBENCH_PY_STRICT=1``: CI's
``log-bench`` job sets it; the nightly Mac warns only, its p99 being the runner's noise as
much as the logger's (docs/performance/logs.md, issue #807). The numbers are printed as
JSON (and written to ``--out``) either way, and the nightly run compares them night to night.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import logging
import os
import subprocess
import sys
import time
from collections.abc import Callable
from typing import TypedDict

BUDGET_P99_NS = 20_000
# the calibration loops' times on the reference machine, measured as Go's logbench.Calibrate's
# references were (the same Mac meets those within 4 %): a hashing and JSON loop, and the
# logger's own kind of work (scrub a line's values with the shared rules, encode it). Until
# issue #744 the first was 0.20 s, ~3.6 times the reference's: every runner up to that slow
# came out at factor 1, CI's (2.8 times slower at logging) among them.
REFERENCE_HASH_S = 0.055
REFERENCE_LINES_S = 0.060
# the p99 of the same work in small chunks, on the reference: a runner's own tail. The budget
# is on a p99, and on a slow runner the tail grows more than the total does (CI, 2026-11-07:
# logging 2.8 times slower at p50 and 3.3 times at p99 on a runner both loops called 2.7).
REFERENCE_TAIL_P99_NS = 7_300
# the p99 of a call through structlog's stock JSON pipeline, on the reference. The loops
# above are C code (hashing, JSON, the regex scrub); the logger's call is mostly the
# interpreter running structlog. On CI's AMD EPYC 7763 runners the logger's p99 was 0.75-0.79
# of a budget set by the loops alone, against 0.55-0.63 on its other runners: the loops ran
# there 2.7 times the reference's time, the logger 3.4 times at p50 and more at p99. The
# stock pipeline's p99 follows it there (the logger's p99 is 1.7 times it on the 7763, the
# 9V74 and the Xeon alike), and a slower logger does not move it (issue #807, 48 runners).
REFERENCE_STOCK_P99_NS = 8_700
# how much slower than the base's logger on the same runner a change may be before its being
# over the budget fails it: a run's rounds agree within 2-3 %, and its ordering (base, head,
# head, base, ...) evens out a runner that drifts during the job
BASE_TOLERANCE = 0.15


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


def _tail_p99_ns(chunks: int = 20_000) -> int:
    """The p99 of the lines loop's work in small timed chunks (the best of three rounds)."""
    from maktaba_pipeline.log import redact

    values = ("http_request", "/api/videos/123?token=abc", "segment 12 of 40 from host-2", "ok")

    def chunk() -> None:
        line: dict[str, object] = {
            "ts": "2026-11-07T10:00:00.000001Z",
            "level": "info",
            "status": 200,
        }
        for n, v in enumerate(values):
            line[f"f{n}"] = redact.scrub(v)
        json.dumps(line)

    for _ in range(2_000):
        chunk()
    clock = time.perf_counter_ns
    best = 0
    for _ in range(3):
        times = [0] * chunks
        for i in range(chunks):
            start = clock()
            chunk()
            times[i] = clock() - start
        times.sort()
        p99 = percentile(times, 0.99)
        best = p99 if best == 0 else min(best, p99)
    return best


def _stock_p99_ns(calls: int = 20_000) -> tuple[int, int]:
    """structlog's stock JSON pipeline (its context merge, the level, an ISO timestamp, the
    JSON renderer) to /dev/null, timed call by call: (p50, p99) of the round with the
    lowest p99, of three. The same library and interpreter paths as the logger, with none of
    its processors, so a slower logger does not move it."""
    import structlog

    devnull = open(os.devnull, "w")  # noqa: SIM115 - closed below
    logger = structlog.wrap_logger(
        structlog.PrintLogger(devnull),
        processors=[
            structlog.contextvars.merge_contextvars,
            structlog.processors.add_log_level,
            structlog.processors.TimeStamper(fmt="iso", utc=True),
            structlog.processors.JSONRenderer(),
        ],
        context_class=dict,
    )
    for _ in range(2_000):
        logger.info("http_request", path="/api/videos/123", status=200, user_id="u-1", took_ms=1.2)
    clock = time.perf_counter_ns
    best = (0, 0)
    for _ in range(3):
        times = [0] * calls
        for i in range(calls):
            start = clock()
            logger.info(
                "http_request", path="/api/videos/123", status=200, user_id="u-1", took_ms=1.2
            )
            times[i] = clock() - start
        times.sort()
        got = (percentile(times, 0.5), percentile(times, 0.99))
        best = got if best == (0, 0) or got[1] < best[1] else best
    devnull.close()
    return best


def calibrate() -> tuple[float, dict[str, float]]:
    """The runner's speed against the reference (the slowest of the three loops, 1–4) and the
    loops' times, which the report shows."""
    raw = {
        "hash_s": _best_of_three(_hash_work),
        "lines_s": _best_of_three(_lines_work),
        "tail_p99_ns": float(_tail_p99_ns()),
    }
    raw["stock_p50_ns"], raw["stock_p99_ns"] = (float(v) for v in _stock_p99_ns())
    f = max(
        raw["hash_s"] / REFERENCE_HASH_S,
        raw["lines_s"] / REFERENCE_LINES_S,
        raw["tail_p99_ns"] / REFERENCE_TAIL_P99_NS,
        raw["stock_p99_ns"] / REFERENCE_STOCK_P99_NS,
    )
    return min(4.0, max(1.0, f)), {k: round(v, 4) for k, v in raw.items()}


def percentile(sorted_ns: list[int], q: float) -> int:
    return sorted_ns[min(len(sorted_ns) - 1, int(q * len(sorted_ns)))]


class BenchResult(TypedDict):
    calls: int
    rounds: int
    p50_ns: int
    p99_ns: int
    p99_ns_rounds: list[int]


def run(calls: int, rounds: int = 3) -> BenchResult:
    """The cost of a call: `rounds` rounds of `calls` calls, judged on the round with the
    lowest p99. A shared runner's other tenants only ever add time to a call, so the best
    round is the logger's own cost, as the calibration's best of three is the runner's speed;
    one noisy round (CI, 2026-11-07: p99 56.5 µs against 54 on a runner 2.7 times slower)
    no longer fails the budget. Every round's numbers are in the report."""
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
    clock = time.perf_counter_ns
    results: list[tuple[int, int]] = []
    for _ in range(max(1, rounds)):
        times = [0] * calls
        for i in range(calls):
            s = clock()
            logger.info(
                "http_request",
                path="/api/videos/123?token=abc",
                status=200,
                user_id="u-1",
                took_ms=1.2,
            )
            times[i] = clock() - s
        times.sort()
        results.append((percentile(times, 0.5), percentile(times, 0.99)))
    devnull.close()
    p50, p99 = min(results, key=lambda r: r[1])
    return {
        "calls": calls,
        "rounds": len(results),
        "p50_ns": p50,
        "p99_ns": p99,
        "p99_ns_rounds": [r[1] for r in results],
    }


class Compared(TypedDict):
    base_p99_ns: int
    head_p99_ns: int
    base_p99_ns_rounds: list[int]
    head_p99_ns_rounds: list[int]
    ratio: float


def _one_round(calls: int, src: str | None) -> tuple[int, int]:
    """One round of `calls` calls in a fresh process, with the logger from `src` (a
    ``pipeline/src``) or the installed one: (p50, p99)."""
    env = dict(os.environ)
    if src:
        env["PYTHONPATH"] = src + os.pathsep + env.get("PYTHONPATH", "")
    out = subprocess.run(
        [sys.executable, __file__, "--one-round", "--calls", str(calls)],
        env=env,
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    got = json.loads(out.strip().splitlines()[-1])
    if src and not os.path.realpath(got["logger"]).startswith(os.path.realpath(src) + os.sep):
        raise RuntimeError(f"the base round ran {got['logger']}, not the logger in {src}")
    return int(got["p50_ns"]), int(got["p99_ns"])


def compare(base_src: str, calls: int, rounds: int = 3) -> Compared:
    """The base's logger (``base_src``, the commit this one is compared with) and this one
    on the same runner, round by round in fresh processes, in the order base, head, head,
    base, ...: a runner that slows or speeds up during the job weighs on both alike. Each is
    judged on its round with the lowest p99, as `run` judges."""
    base: list[int] = []
    head: list[int] = []
    for i in range(max(1, rounds)):
        order = (base_src, None) if i % 2 == 0 else (None, base_src)
        for src in order:
            (base if src else head).append(_one_round(calls, src)[1])
    b, h = min(base), min(head)
    return {
        "base_p99_ns": b,
        "head_p99_ns": h,
        "base_p99_ns_rounds": base,
        "head_p99_ns_rounds": head,
        "ratio": round(h / b, 3),
    }


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument(
        "--calls", type=int, default=int(200_000 * float(os.environ.get("LOGBENCH_SCALE", "1")))
    )
    ap.add_argument("--out", default=os.environ.get("LOGBENCH_PY_OUT", ""))
    ap.add_argument("--rounds", type=int, default=int(os.environ.get("LOGBENCH_PY_ROUNDS", "3")))
    ap.add_argument(
        "--base-src",
        default=os.environ.get("LOGBENCH_PY_BASE_SRC", ""),
        help="the pipeline/src of the commit to compare with, measured on the same runner",
    )
    ap.add_argument("--one-round", action="store_true", help=argparse.SUPPRESS)
    args = ap.parse_args(argv)
    if args.one_round:  # a child of compare: one round, and which logger it ran
        from maktaba_pipeline import log as mlog

        one = run(args.calls, 1)
        print(  # noqa: T201
            json.dumps({"p50_ns": one["p50_ns"], "p99_ns": one["p99_ns"], "logger": mlog.__file__})
        )
        return 0
    factor, raw = calibrate()
    got = run(args.calls, args.rounds)
    limit = int(BUDGET_P99_NS * factor)
    report: dict[str, object] = {
        "logger_overhead_python": got,
        "_runner": {"factor": round(factor, 2), "budget_p99_ns": limit, "calibration": raw},
    }
    vs: Compared | None = None
    if args.base_src:
        try:
            vs = compare(args.base_src, args.calls, args.rounds)
        except (subprocess.CalledProcessError, RuntimeError, ValueError, KeyError) as e:
            print(f"::warning::the base logger could not be measured: {e}", file=sys.stderr)  # noqa: T201
        else:
            report["vs_base"] = vs
    text = json.dumps(report, indent=2)
    print(text)  # noqa: T201
    if args.out:
        with open(args.out, "w") as f:
            f.write(text)
    return verdict(got["p99_ns"], limit, factor, vs)


def verdict(p99: int, limit: int, factor: float, vs: Compared | None) -> int:
    """Over the budget fails with LOGBENCH_PY_STRICT=1, unless the base, measured on the same
    runner, is as slow: then it is the runner, not the change (#806 failed twice at factor
    2.7 with no change to the logger, while the commit with its code passed elsewhere). The
    runner's factor follows the logger only within ~15 % across CI's CPUs (issue #807), so
    with a base the budget fails a change only when it is also more than BASE_TOLERANCE
    slower than the base; slower than that, under the budget, warns."""
    strict = os.environ.get("LOGBENCH_PY_STRICT", "") == "1"
    over = p99 > limit
    slower = vs is not None and vs["ratio"] > 1 + BASE_TOLERANCE
    budget = (
        f"a Python log call (p99): {p99 / 1000:.1f} µs, budget {limit / 1000:.1f} µs "
        f"(20 µs at the reference, runner factor {factor:.2f})"
    )
    base = (
        ""
        if vs is None
        else f"; the base's on this runner: {vs['base_p99_ns'] / 1000:.1f} µs "
        f"(this one {vs['ratio']:.2f} times it)"
    )
    if over and (vs is None or slower):
        print(f"::{'error' if strict else 'warning'}::{budget}{base}", file=sys.stderr)  # noqa: T201
        return 1 if strict else 0
    if over:
        print(f"::warning::{budget}{base}: the runner, not this change", file=sys.stderr)  # noqa: T201
    elif slower:
        print(  # noqa: T201
            f"::warning::slower than the base, within the budget: {budget}{base}", file=sys.stderr
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
