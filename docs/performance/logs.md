# The log path's performance budget

Logging must not slow playback or fill the disk. Epic 53 story 53.13 sets the numbers and
`cmd/maktaba-server/internal/logbench` measures them. The benchmarks are tests behind the
`logbench` build tag; CI's `log-bench` job runs them on a change to the log path
(`shared/log/**`, the keeper, the index, the supervisor, the pipeline's `log/`).

## The targets (the reference machine, an Apple M2)

| Bench | What | Target | CI load (`LOGBENCH_SCALE=1`) |
|---|---|---|---|
| capture | the keeper's CPU per line | ≤ 20 µs a line (500 lines/s within 1 % of a core); no line lost; every warn and error kept | 50,000 lines from 4 children (10 s of 5,000 lines/s) |
| index catch-up | the index's cost per line | ≤ 400 µs a line (a 5,000/s minute caught up within 2 minutes) | 300,000 lines over 7 days |
| index lag | a line written to searchable | ≤ 2 s at p95 | 500 lines and one pass |
| search, filters | 24 h, one service, warn and above | ≤ 300 ms at p95 | the same corpus |
| search, full text | words, including Arabic | ≤ 1.5 s at p95 | the same corpus |
| search, request | one request id | ≤ 300 ms at p95 | the same corpus |
| dashboard, 30 days | the Overview board's 30 days of hours | ≤ 500 ms at p95 | the overview's series, 5 label sets each |
| Go logger | one log call | ≤ 20 µs at p99 | 200,000 calls |
| disk | the segments under their cap through a day | at most the cap and the hour still open; the newest warn lines kept | a 4 MB cap, 30 hours |

A budget is multiplied by the runner's speed against the reference (`logbench.Calibrate`).
The factor is the larger of two loops: a fixed hashing loop, and the keeper's own kind of
work, decoding, scrubbing and encoding 5,000 log lines. A runner's SHA instructions can make
hashing as fast as the reference while the keeper's work is slower. The factor is clamped to
1–4, as `test-budget` does. A runner half as fast is not failed for being slow, and a change
that doubles a cost still fails on any runner. Capture is budgeted by the process's CPU time
per line (user and system), since its target is a share of a core. The wall clock is
stretched by a busy runner.

The story's full loads (a 60 s burst, a 15 M-line corpus) are `LOGBENCH_SCALE=6` and up.
They take tens of minutes and are meant for a Mac by hand, not for CI.

## Running them

```sh
cd cmd/maktaba-server
go test -tags logbench -count=1 -v ./internal/logbench/              # the CI loads (~1 min)
LOGBENCH_SCALE=6 go test -tags logbench -count=1 -v -timeout 2h ./internal/logbench/
```

Each bench prints its numbers as JSON at the end (`LOGBENCH_OUT=<file>` also writes them to
a file, which the CI job keeps as an artifact). The corpus is seeded (`logbench.Corpus`), so
two runs read the same lines.

## The Python logger

`pipeline/tools/log_bench.py` times the pipeline's logger (contextvars, the shared redaction,
the ring, the JSON renderer) writing to `/dev/null`: 200,000 calls at the CI load
(`LOGBENCH_SCALE` scales it), ≤ 20 µs at p99 on the reference, times the runner's factor:
the slower of two loops (hashing and JSON, and the logger's own work: scrub a line's values,
encode it) against their times on the reference, clamped to 1–4, as `logbench.Calibrate`
does. The report shows both loops' times (`_runner.calibration`). The `log-bench` job runs
it after the Go benches and keeps its numbers (`logbench-python.json`).

Over budget it warns rather than fails, as the story's rollout starts a budget;
`LOGBENCH_PY_STRICT=1` makes it fail.

Issue #744 took a call from 19.5 to 11.5 µs at p50 (27 to 17 µs at p99) on the reference
class of Mac, an M3 Ultra; CI's runner was 55 µs at p50 and 82 µs at p99 before it:

| What | Before | After |
|---|---|---|
| The redaction of a line | 8.7 µs | 4.7 µs |
| The ring's copy (a second JSON encoding) | 2.5 µs | none: the console's line is kept |
| The time stamp | 0.9 µs | 0.3 µs |

- **The needs.** A value's check against the rules' literal needs was a case-insensitive
  regex alternation: about 1 µs on a 12-character value. It is now `in` on the lowered text,
  in a loop (an `any()` over a generator cost twice as much).
- **The keys.** What each key gets (the logger's own, scrubbed, masked when a string, masked)
  is decided once per key and kept.
- **The user hash.** `user_h`, a keyed hash the logger makes itself, is added after the
  scrub. As 64 hex characters it had enough digits to go through every rule.
- **The ring.** The ring keeps the line the JSON renderer wrote for the console, byte for
  byte, so a line is encoded once.
- **The time stamp.** `ts` is written as structlog's ISO stamper wrote it, with the date and
  time formatted once a second.

The factor was wrong as well. The hashing loop's reference was 0.20 s, about 3.6 times the
reference machine's 0.055 s, so every runner up to that slow came out at factor 1. That
included CI's, which is about 2.8 times slower at logging.

What is left of a call is structlog's own (≈ 2.8 µs with no processors), the contextvars
merge (≈ 1.6 µs), the scrub of a value that holds a secret (≈ 2.6 µs), and the encoding and
the write.

## Every night on a Mac

`.github/workflows/nightly-perf.yml` runs the Go and Python benches at 04:17 UTC on a
macOS arm64 runner (the reference class), keeps the numbers for 90 days (`logbench-nightly`),
and compares them with the last successful night's (`tools/logbench/compare.py`). Each run is
divided by its runner factor first. A cost more than 20 % higher opens the issue "Log benches:
a cost grew by more than 20 % overnight", or comments on it while it is open. A bench over its
budget fails the run (the Python bench warns, as above).

## Not measured here yet

- The `logs` role's CPU share and memory at a steady 500 lines/s on a live home.
