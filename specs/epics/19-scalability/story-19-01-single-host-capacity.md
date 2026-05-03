# Story 19.1 — Single-host capacity floor

Establish what "one Mac mini" must handle and assert it.

## Acceptance criteria

- AC1. Reference deployment (Mac mini M2, 16 GB RAM, 30 TB external SSD,
  Postgres) sustains:
  - 50,000 videos in the catalog.
  - 1,000,000 transcript segments indexed.
  - 8 concurrent direct-play streaming sessions, or
  - 4 concurrent transcoded streaming sessions.
  - 1 concurrent transcribe + 4 concurrent index workers in the pipeline.
- AC2. The library landing page (first 50 videos, paginated) loads end-to-
  end in p95 ≤ 500 ms with the catalog loaded.
- AC3. The system survives a 30 TB initial scan without exhausting
  memory or running out of FDs (`ulimit -n` ≥ 4096 documented).
- AC4. The capacity floor is asserted by a `make capacity` target that
  loads the seeded fixture, runs the workload mix for 30 minutes, and
  fails on any budget breach (RSS, RPS, error rate ≤ 0.1 %).

## Test cases

- TC1. Catalog walk: paginate through all 50 k videos at 50 per page;
  total walk completes in ≤ 5 minutes with stable RSS.
- TC2. Concurrent playback: open 8 direct-play sessions on distinct
  videos, hold for 10 minutes; no buffer underrun event recorded.
- TC3. Mixed workload: 8 streams + 1 active transcribe + 100 search qps;
  p95 search budget from Epic 18 still holds.

## Edge cases

- EC1. Backing storage on slow USB (≤ 50 MB/s sequential) — direct play
  ladder is capped at 720p; documented and asserted.
- EC2. macOS file-watch limits: `watchdog` falls back to polling when
  inotify-equivalent FDs exhaust; the test forces the failure and
  verifies the polling fallback still discovers new files within 60 s.
- EC3. SQLite mode: capacity floor is documented at 1/4 of Postgres
  (~12 k videos, ~250 k segments) before write contention degrades.
