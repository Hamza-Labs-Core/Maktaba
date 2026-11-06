# The log path's performance budget

Logging must not slow playback or fill the disk. Epic 53 story 53.13 sets the numbers and
`cmd/maktaba-server/internal/logbench` measures them. The benchmarks are tests behind the
`logbench` build tag; CI's `log-bench` job runs them on a change to the log path
(`shared/log/**`, the keeper, the index, the supervisor, the pipeline's `log/`).

## The targets (the reference machine, an Apple M2)

| Bench | What | Target | CI load (`LOGBENCH_SCALE=1`) |
|---|---|---|---|
| capture | the keeper's cost per line | ≤ 20 µs a line (500 lines/s within 1 % of a core); no line lost; every warn and error kept | 50,000 lines from 4 children (10 s of 5,000 lines/s) |
| index catch-up | the index's cost per line | ≤ 400 µs a line (a 5,000/s minute caught up within 2 minutes) | 300,000 lines over 7 days |
| index lag | a line written to searchable | ≤ 2 s at p95 | 500 lines and one pass |
| search, filters | 24 h, one service, warn and above | ≤ 300 ms at p95 | the same corpus |
| search, full text | words, including Arabic | ≤ 1.5 s at p95 | the same corpus |
| search, request | one request id | ≤ 300 ms at p95 | the same corpus |
| dashboard, 30 days | the Overview board's 30 days of hours | ≤ 500 ms at p95 | the overview's series, 5 label sets each |
| Go logger | one log call | ≤ 20 µs at p99 | 200,000 calls |
| disk | the segments under their cap through a day | at most the cap and the hour still open; the newest warn lines kept | a 4 MB cap, 30 hours |

A budget is multiplied by the runner's speed against the reference (`logbench.Calibrate`:
a fixed hashing loop, the factor at least 1 and at most 4), as `test-budget` does. A
runner half as fast is not failed for being slow. A change that doubles a cost still fails
on any runner.

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

## Not measured here yet

- The Python logger's cost per call.
- The `logs` role's CPU share and memory at a steady 500 lines/s on a live home.
- A nightly run on a Mac runner, with an issue opened on a regression over 20 %.
