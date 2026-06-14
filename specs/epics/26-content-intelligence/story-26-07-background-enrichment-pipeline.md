# Story 26.7 — Background enrichment pipeline

## Description

Wire Stories 26.1–26.6 into the processing pipeline as a coherent
`classify → match → enrich → group` flow, with the right things on and
off the critical path:

- **`classify`** is a **new top-level stage** in the canonical pipeline
  ([Epic 01 Story 1.6](../01-scanner/story-01-06-video-state-machine.md)),
  inserted between `index` and `thumbnail`. It runs the parser (26.1) and
  transcript topic/entity extraction (26.2) — all **local, offline,
  fast**. A successful `classify` moves the video to a new `CLASSIFIED`
  state. This is safe on the critical path.
- **`enrich`** is an **out-of-band job** on its own queue. When
  `classify` finishes, it fire-and-forgets an `enrich_job`. Enrichment
  (26.5) is networked and rate-limited and **must never block the video
  reaching `READY`**. The video's state machine does not wait on it.
- **`group`** is two **library-level debounced passes** — series
  detection (26.3) and the auto-collection builder (26.4) — triggered
  after a batch of videos finish `classify`/`enrich`.

This story owns the stage registration, the `enrich_jobs` queue, the
debounced group triggers, rate-limit/cache wiring, and the re-enrich
entry point.

## Stage & state placement

- `processing_jobs.stage` gains `classify`; `videos.state` gains
  `classified`. The seven-stage path becomes
  `scan → audio → transcribe → subtitle_gen → index → classify →
  thumbnail`. `classify` slots after `index` because topic/entity work
  reuses the unit embeddings `index` wrote to Chroma.
- `enrich` is **not** a `processing_jobs.stage` and **not** a
  `videos.state`. It is its own `enrich_jobs` table with its own
  worker, status, attempts, and backoff — modeled on the existing job
  semantics but decoupled so it can run slow without holding a video out
  of `READY`.

## Acceptance criteria

- After `index`, the orchestrator enqueues a `classify` stage job; on
  success the video transitions to `CLASSIFIED`, then proceeds to
  `thumbnail` → `READY` exactly as before for the remaining stages.
- `classify` failure is **isolated**: like chapter inference
  ([Story 5.7](../05-search-indexing/story-05-07-chapter-inference.md)),
  a failure is logged (`kind=classify_failed`), recorded on the video's
  metrics, and the video **still advances** to `thumbnail`/`READY`.
  (Classification is an enhancement, not a gate.)
- On `classify` done, an `enrich_job` is enqueued **only if**
  `settings.enrich.enabled` and at least one provider key is configured;
  otherwise enrichment is skipped and the video still reaches `READY`.
- The `enrich` worker respects per-provider rate limits, the shared
  cache, retries with backoff, and a daily cap (all from 26.5); a
  rate-limited or offline box defers enrich jobs without failing them.
- Series detection and auto-collection passes are **debounced** at the
  library level (default 30 s after the last `classify`/`enrich` in a
  batch) and **coalesced** (many videos → one pass), and can be
  triggered on demand (maintenance action / admin).
- **Re-enrich** (`POST /api/videos/{id}/enrichment/reenrich`) and a
  library-level "re-enrich all" enqueue fresh `enrich_jobs` that refresh
  by stored `external_id` where one is accepted (idempotent), else
  re-search.
- Re-`classify` is triggered automatically when the active transcript
  changes (Epic 3 Story 3.5) or the `parser_version`/`model_version`
  bumps; it replaces classification atomically.
- The pipeline is **resumable**: a crash mid-`classify` re-runs the
  stage; a crash mid-`enrich` re-runs the enrich job (idempotent via
  `external_id` + cache). No half-applied state.
- Ordering guarantee: enrich for a video never runs before that video's
  `classify` (enrich needs the parsed title + content type).

## Test cases

- `test_classify_stage_registered` — a fresh video runs through
  `…→index→classify→thumbnail→READY`; `CLASSIFIED` state observed.
- `test_classify_failure_does_not_block_ready` — inject a classify
  fault → video still reaches `READY`; `classify_failed` metric set.
- `test_enrich_enqueued_after_classify` — with a provider key set,
  `classify` done → exactly one `enrich_job` enqueued.
- `test_enrich_skipped_without_key` — no keys → no enrich job; video
  still `READY`.
- `test_enrich_does_not_gate_ready` — hang/stall the enrich worker →
  video reaches `READY` regardless.
- `test_group_passes_debounced_and_coalesced` — import 50 episodes →
  exactly one series-detect + one auto-collection pass fire (not 50).
- `test_reenrich_idempotent` — re-enrich an accepted match → refresh by
  id, no duplicate candidate.
- `test_reclassify_on_transcript_change` — flip active transcript →
  `classify` re-runs, classification replaced atomically.
- `test_enrich_before_classify_never` — assert ordering: no enrich job
  is dispatched for a video lacking a completed `classify`.
- `test_resume_after_crash` — kill the worker mid-`classify` and
  mid-`enrich`; both resume cleanly with no orphaned/half state.
- `test_daily_cap_defers` — exceed a provider's daily cap → remaining
  enrich jobs deferred to the next window, not failed.

## Edge cases

- **Offline box.** `classify` runs (local); `enrich` jobs queue and
  back off; nothing fails; videos reach `READY`. When connectivity
  returns, deferred enrich jobs drain.
- **Provider key added later.** Already-`READY` videos are not
  auto-enriched retroactively unless the operator runs "re-enrich all";
  newly imported videos enrich normally.
- **Huge import.** The debounce + coalesce keep group passes O(batches),
  not O(videos); enrich is rate-limited so a 10k import doesn't blow the
  TMDb quota in one burst.
- **`classify` enabled mid-life.** Turning on `settings.classify` for an
  existing library enqueues a backfill of `classify` jobs for `READY`
  videos without re-running earlier stages.
- **Stage ordering vs. legacy.** Existing libraries created before Epic
  26 have no `classify` history; the backfill path classifies them
  without disturbing their `READY` state.
- **Enrich job storms.** A flapping provider breaker (26.5) pauses that
  provider's jobs but lets others proceed; jobs aren't lost.
- **Group pass during active import.** A pass that starts while more
  videos are still classifying re-debounces to the end of the batch
  rather than producing partial groupings repeatedly.
