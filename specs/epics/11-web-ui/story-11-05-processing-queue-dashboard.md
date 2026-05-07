# Story 11.5 — Processing queue dashboard (progress bars, pause / resume controls)

A `/queue` page that renders the live worker pool: jobs grouped by stage,
per-job progress bar, ETA, retry / pause / resume / cancel actions. Backed
by `GET /api/jobs`, `WS /ws/jobs`, and `GET /api/queue/stats`.

**Anchors:** [`architecture.md` §7.10](../../architecture.md). Depends on
Epic 7 Stories 7.12 (job control), 7.13 (queue stats), 7.16 (WS); Epic 1
Stories 6.4–6.10 (queue control).

## API surface (canonical)

The dashboard uses the canonical `/api/queue/stats`, `/api/jobs/{id}`, and
`/ws/jobs` endpoints from Epic 7 (architecture §9.5). It does **not** use
the parallel `/api/processing/*` namespace from Epic 21 Story 21.7 — that
duplication was resolved by [REVIEW §1.2.a](../../REVIEW.md) in favor of
the Epic 7 names; Story 21.7 is now expressed as additional fields on the
canonical endpoints.

## AC

- Page loads with the current state of all jobs in the last 24 hours by
  default; "Show all" expands history.
- Each job row: video poster + title, stage badge (`scan, probe, extract,
  transcribe, subtitle_gen, index, thumbnail` — the canonical stage names
  per [REVIEW §1.3.b/c](../../REVIEW.md)), state badge, progress bar with
  `processed_seconds / total_duration_seconds`, ETA, attempts counter.
- Inline actions per job: Pause, Resume, Cancel, Retry (Retry only on
  `failed`), "Move to front of queue" (priority bump).
- Bulk actions: select multiple jobs → Pause all / Resume all / Cancel all.
- Per-stage cards at the top: pending / running / done / failed counts;
  click filters the list. Counts also surface `oldest_pending_age_sec`
  (the metric Story 21.7 asked for) and rely on the covering index
  recommended in [REVIEW §6.1](../../REVIEW.md):
  `processing_jobs (state, finished_at) WHERE state IN ('done','failed')`
  and `processing_jobs (state, created_at) WHERE state='pending'`.
- Live updates: WS `job.progress` events update the progress bar without
  a refetch; UI throttles renders to ≤ 1 Hz per visible job (architecture
  §7.10).
- Force-pause: when a job is stuck in `pause_requested = true` for >
  `pause_grace_sec`, a "Force pause" button appears (architecture §7.7).
- Empty state: "No jobs running" with a "Process all unindexed videos" CTA
  for admins.
- Heartbeat indicator uses the canonical 5 s interval (per
  [REVIEW §1.4.c](../../REVIEW.md)); a job with no heartbeat for ≥ 90 s
  shows a "stale — being reaped" badge.

## TC

- 50 jobs running in parallel: the page stays responsive; CPU < 10% on the
  client.
- Click Pause on a transcribe job: within 60 s the job enters `paused` at
  the next segment boundary; the row re-renders with the resume offset.
- Pause then immediately Resume: idempotency holds — the job either
  resumes from the paused offset or, if pause hasn't taken effect yet,
  ignores the resume (resume on a `running` job is a no-op).
- Disconnect WS for 15 s, reconnect: the dashboard does a one-shot
  re-fetch and reconciles state.
- Retry a `failed` job: `attempts` resets to 0, state goes `pending`.

## EC

- A job's `last_segment_end_sec` decreases between snapshots (server bug):
  detect and log; do not animate the bar backwards.
- ETA jitters because `realtime_factor` is unstable on a fresh job: don't
  show ETA until at least 3 segments have committed.
- 1,000+ jobs: the list virtualizes; bulk-select uses a server-side filter
  expression rather than a client-side ID list.
- Network partition during a bulk Pause: show "12 of 50 paused — retry the
  rest?" rather than a silent partial success.
