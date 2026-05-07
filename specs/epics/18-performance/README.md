# Epic 18 — Performance

**Goal.** Maktaba feels snappy on a single Mac mini / NAS-class host with a
30 TB library. User-facing latency budgets are explicit, measured in CI on
representative hardware, and regress-tested. Hot paths (search, manifest
issue, range serve, WS event) hit cache; cold paths (cold transcode, cold
embed) are bounded and observable.

This epic does **not** cover scale beyond a single household — that's
[Epic 19](../19-scalability/README.md). It covers what "fast enough" means
at one box.

This document is the normative source for v1 latency budgets. When
[`specs/architecture.md`](../../architecture.md) gives the same number,
the two must agree; resolution PRs update both in lockstep.

## Stories

- [Story 18.1 — Define and codify latency budgets](story-18-01-latency-budgets.md)
- [Story 18.2 — Search end-to-end performance](story-18-02-search-performance.md)
- [Story 18.3 — Streaming hot-path performance](story-18-03-streaming-hot-path.md)
- [Story 18.4 — Pipeline throughput targets](story-18-04-pipeline-throughput.md)
- [Story 18.5 — Memory and CPU envelopes](story-18-05-memory-cpu-envelopes.md)
- [Story 18.6 — Client-perceived performance](story-18-06-client-perceived-performance.md)
- [Story 18.7 — Database query performance and N+1 prevention](story-18-07-database-query-performance.md)
- [Story 18.8 — Cache layout and hit-rate floors](story-18-08-cache-layout-hit-rates.md)

## Conventions

- **Story** — a discrete unit of work small enough to land in 1–3 PRs.
- **Acceptance criteria** (AC) — checks that must hold before the story is
  marked done. Each AC is independently verifiable.
- **Test cases** (TC) — concrete, named scenarios the test suite will cover
  for the story. Format: *given / when / then* compressed to one or two
  sentences.
- **Edge cases** (EC) — known-tricky inputs or environmental conditions the
  implementation must explicitly handle.
- **Service identifiers** — `api` (Go), `streaming` (Go), `pipeline`
  (Python), `web` (TypeScript), `apps/*` (Capacitor / Tauri / Swift /
  Kotlin).
