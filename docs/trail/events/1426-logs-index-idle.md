---
schema: 1
kind: pr
pr: 1426
title: "The log index sits idle: closed hours are read once, one writer, metrics count against the cap"
date: 2026-11-15
session: 2efaf21d-936d-4aab-b195-39d64fd998de
agent: claude-code
---

## What happened
- On the live home, `maktaba-server logs` from v0.3.135 had used 150–300% CPU nonstop for
  26 hours. Its own log repeated `metrics_record_failed: database is locked (5) (SQLITE_BUSY)`.
  The live process was only read, never restarted or changed.
- Cause: `Ingest` runs every 200 ms, and it skipped "nothing new" only for open `.ndjson`
  hours. Each pass opened every closed `.zst` hour (about 3,500 live), started a default zstd
  decoder (one goroutine per core) and decompressed from byte 0 to the stored position. It
  also ran one `SELECT` per segment. That matches the malloc and GC frames in `sample`.
- Reproduced on a synthetic root built like the live one: 20 services × 7 days, 3,360 closed
  hours and a 565 MB index. The idle binary from main used 285.7% of a core (85.7 CPU-s in
  30 s). #1426 brings it to 0.8% (0.53 CPU-s in 60 s). An idle pass went from about 6 ms to
  215 µs, and from 17.5k to 338 allocations.
- Reading the live `logs.db` read-only found a second problem: 366 of its 560 MB was
  `metric_1m` (2M rows). The 350 MB cap evicted log lines only, so it had deleted almost every
  info line (23k left, against 183k warn and 155k error) and the file still stayed over the
  cap. Segments were pruned correctly. `auto_vacuum` was INCREMENTAL with an empty freelist,
  so no one-time repair or admin tool was needed: the file shrinks once old minutes go.
- Fix (#1426, merge a49616163):
  - A closed hour read to its end is marked in `positions.size` and never opened again.
  - Positions are kept in memory.
  - A directory is listed again only when its mtime or size changes.
  - Writes are serialized on one mutex (`Index.write` and `Index.exec`).
  - Over the cap, metric minutes older than a day go before info lines, and long board
    ranges read the hours.
- Tests: `TestIdlePassOpensNoClosedHour`, `TestOpenHourReadsOnFromItsPosition`,
  `TestRecordWaitsForAnotherWriter` (fails with the live SQLITE_BUSY text when the mutex is
  removed), `TestCapTrimsOldMinutesBeforeInfoLines`, `BenchmarkIdlePass`. Each was checked
  by mutation.
- CI on the way:
  - The first push failed `unit / unit` on the test budget, from my own tests. Under
    `-race`, `TestCapTrimsOldMinutesBeforeInfoLines` wrote 432k metric rows and took 1m46s,
    and `TestIdlePassOpensNoClosedHour` took 1.47s, against a 1.2 s hard cap (run
    43743341725). The tests were cut to a few dozen fat rows (about 0.4 s) and 26 segments
    (2815b0f40), and both still fail with their fix removed.
  - The same run's Cloud rehearsal `drain-1k` failed on 1 of 1,155 requests ("short
    segment: 0 of 262144 bytes", run 43743341541). That code is unrelated to the log index,
    and the next run passed.
  - On 2815b0f40, two tests this PR doesn't touch broke the hard cap and passed on re-run
    (run 43745326693): `cmd/maktaba-launcher::TestANewerAppTakesOverFromTheKeptCopy` (1.45s
    alone) and `shared/logkeeper/go::TestRollAndCompress` (1.25s alone).
  - Main's CI unit job also failed at 15:58 and 16:49 that day.

## What we learned
- A "nothing new" check that covers only one file form is a hot loop waiting for the other
  form to pile up. Here it waited for a week of compressed hours.
- `busy_timeout` doesn't make several in-process writers safe when one of them holds the lock
  for long (the sweep's deletes and `wal_checkpoint(TRUNCATE)`). Serializing in Go does.
- A size cap that evicts one kind of data while another kind fills the file deletes all of
  the first and never meets the cap.

## Keep doing
- Read the live store read-only (`sqlite3 -readonly`, `dbstat`) before guessing at a cause.
  It found the retention bug that the CPU symptom hid.
- Run new Go tests under `go test -short -race` locally before pushing: the unit tier's
  per-test cap applies to race-instrumented SQLite, which is about 20× slower.
- Measure an idle service's steady-state CPU on a root of production shape, before and
  after, from the same starting data.

## Open
- `TestANewerAppTakesOverFromTheKeptCopy` and `TestRollAndCompress` run over the unit tier's
  1.2 s hard cap even alone on a CI runner. Trim them before they keep failing other PRs.
- The live home gets the fix with the next release. After it updates, confirm that the logs
  role sits near idle and that `logs.db` drops under its cap at the next hourly sweep.
