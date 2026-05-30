# Epic 06 — Job Queue: Spec-vs-Implementation Gap Analysis

**One-line verdict:** The queue primitives are real and the daemon
(`__main__.py` → `runtime.run()`) genuinely instantiates `ClaimLoop` +
`Reaper` + a dispatch — the prior audit's "sleep-loop stub" premise is
**false** — but the worker dispatch is no-op placeholders that always
`mark_done`, so backoff/retry, cooperative pause/cancel, force-pause,
graceful shutdown, real concurrency caps, and observability are all
**defined-but-unwired**; and the Go control endpoints write a
nonexistent `updated_at` column and emit none of the required notifies,
so the control plane is broken end-to-end against the real schema.

## Runtime reachability (the audit's central question)

`pipeline/src/maktaba_pipeline/__main__.py:215` → `_serve()` →
`runtime.run()` (`runtime.py:238`). `run()` builds `ClaimLoop`
(`runtime.py:279`) and `Reaper` (`runtime.py:287`), calls
`reaper.start()` and `await claim_loop.run()`. This is a **real
worker boot path**, not a stub. The `__main__` docstring's claim of a
heartbeat ticker + reaper is accurate for claim+reaper+heartbeat.

**However**, `build_default_dispatch` (`runtime.py:176-215`) installs
`_placeholder_handler` for every stage; `_placeholder_handler`
(`runtime.py:218-235`) only logs and calls `mark_done`. No production
caller passes `dispatch_overrides` (grep: only tests/self). And
`ClaimLoop._run_job` (`runner.py:230-253`) on exception only logs +
re-raises; it never calls `mark_failed_or_retry`. Net: a booted worker
drains the queue by marking everything `done` and never exercises
retry, pause, cancel, force-pause, shutdown drain, GPU locks, or
metrics.

## Per-story AC tables

### Story 6.1 — Schema, migration, indexes

| AC | Status | Evidence |
|----|--------|----------|
| `processing_jobs` per §7.1 + 4 indexes | complete | `0002_processing_jobs.sql:32-126`; claim/video-stage/reaper/pause indexes all present (`:98,103,109,115`) |
| `stage` CHECK = canonical enum | complete | `0002_processing_jobs.sql:68-73` exact 7-value enum |
| Same migration on PG + SQLite | complete | `0002_processing_jobs.sqlite.sql:57-100` mirrors with type swaps, bool CHECK `IN (0,1)` |
| `enqueue()` writes one row, idempotent, skip-when-done | complete | `db/jobs.py:217-301`; done-skip via `_DONE_ROW_SQL`, reuse via unique partial index `ON CONFLICT DO NOTHING` |
| `NOTIFY jobs.new` after insert `{id,video_id,stage,priority}` | complete | PG trigger `0002:129-151`; SQLite manual publish `jobs.py:276-288`; channel `pubsub.py:39` |

Verdict: **complete**. Strongest story.

### Story 6.2 — Claim loop

| AC | Status | Evidence |
|----|--------|----------|
| PG claim = §7.3 single UPDATE w/ FOR UPDATE SKIP LOCKED | complete | `db/jobs_claim.py:63-82` |
| SQLite claim = asyncio lock + BEGIN IMMEDIATE | complete | `jobs_claim.py:104-170`, `_get_sqlite_claim_lock` |
| Returns `Job` or `None` | complete | `claim_one:173-196`, `_row_to_job` |
| Accepts pending+paused where `pause_requested=false` | complete | `_CLAIM_SQL_PG:72-74` `state IN ('pending','paused') AND pause_requested=false` |
| Respects cancel/`not_before`/stage filter | complete | `:73-76` |
| Sets claimed/claimed_by/claimed_at/attempts+1 | complete | `:65-69` |
| Driven by LISTEN jobs.new / poll | complete | `wakeup.py` PgListenWakeup/PubsubWakeup; wired in `runner.py:135-139` |

Verdict: **complete** (primitive + wired into the booted loop).

### Story 6.3 — Heartbeat & progress

| AC | Status | Evidence |
|----|--------|----------|
| `tick(...)` single UPDATE bumps progress + heartbeat | partial | `db/jobs_progress.py:136-173`; **signature mismatch**: spec says `tick(job_id, processed_seconds_delta, ...)` (delta); impl `ProgressTick.processed_seconds` is absolute (`jobs_progress.py:51-73` docstring). No `tick()` wrapper with the spec arg list exists. |
| Pure heartbeat every `heartbeat_sec` for non-transcribe | partial | `HeartbeatTask` (`heartbeat.py:41-93`) works, but `build_default_dispatch` calls `heartbeat_for(db, job_id=job.id)` (`runtime.py:210`) **without passing `cfg.heartbeat_sec`** — CLI `--heartbeat-sec` never reaches the cadence; always 5 s default |
| `jobs.progress` payload exactly §7.10 | complete | trigger `0028:23-38` + SQLite `_build_progress_payload:198-218` match incl. `updated_at` key |
| pure heartbeat fires `jobs.heartbeat` not `jobs.progress` | complete | `0028:39-48` ELSIF branch; `tick_heartbeat:176-195` |
| no legacy singular channel names | complete | `pubsub.py:39-44` plural only; grep found no `job.progress` etc. in src |

Verdict: **partial**. Primitive correct; arg-contract & config-propagation gaps. Since dispatch is a placeholder, real per-segment `tick_progress` is never invoked at runtime (no caller of `tick_progress` outside tests/stt).

### Story 6.4 — Pause, resume, cancel via request flags

| AC | Status | Evidence |
|----|--------|----------|
| `POST /jobs/{id}/pause` sets `pause_requested`, emits `jobs.flag_set {id,flag:'pause'}` | partial | `api/.../jobs/jobs.go:146-189` sets flag BUT **no `NOTIFY jobs.flag_set`**; UPDATE writes `updated_at=$2` — **column does not exist** in `0002` (no migration adds it) → statement errors on real PG |
| `?force=true` sets state=paused, paused_at_sec=last_segment_end_sec, claimed_by=NULL WHERE state IN (claimed,running,resuming) + `NOTIFY jobs.force_pause` | missing | `jobs.go:154-159`: `WHERE state IN ('running','pending')` (misses `claimed`/`resuming`, wrongly includes `pending`), sets `paused_reason='user-force'`, **never sets `paused_at`/`paused_at_sec`**, **no `jobs.force_pause` notify** |
| `POST /resume` clears flag, no state change | missing | `jobs.go:192-216` actively sets `state='pending'` + `not_before` — spec says **no state change**; also no `jobs.flag_set` |
| `POST /cancel` sets `cancel_requested`, emits `jobs.flag_set` | partial | `jobs.go:219-242` sets flag, no notify, `updated_at` bug |
| Worker per-segment `SELECT pause_requested,cancel_requested` | unwired | `db/jobs_state.read_flags:107-122` + `control.should_pause/should_cancel` exist but **no runtime caller**; placeholder dispatch never polls |
| `ForcePauseListener` aborts subprocess | unwired | `control.py:52-181` complete but never instantiated by `runtime.run()` |

Verdict: **missing/broken**. API contract violated (notifies absent, `updated_at` schema error, force-pause SQL wrong, resume mutates state). Worker-side cooperative check unwired.

### Story 6.5 — Backoff and retry

| AC | Status | Evidence |
|----|--------|----------|
| `attempts < max_attempts` → pending + `not_before=now()+backoff` + structured error JSON | unwired | `jobs_state.mark_failed_or_retry:271-353` implements this correctly, but `runner._run_job` re-raises instead of calling it; placeholder dispatch never fails → **never reached at runtime** |
| Backoff `min(60·2^(n-1),3600)±25%` | complete | `backoff.compute_backoff:36-54` exact formula |
| `attempts >= max_attempts` → failed terminal | complete (logic) / unwired (runtime) | `jobs_state.py:306-321` |
| `error.retryable=false` → failed immediately | complete (logic) | `jobs_state.py:306` `error.retryable and attempts < max_attempts` |
| `POST /jobs/{id}/retry` resets failed→pending, attempts=0, not_before=NULL, error=NULL | partial | `jobs.go:245-279` resets correctly EXCEPT writes `updated_at=$2` (nonexistent column → errors on real PG). Python `retry_failed:389-396` correct |

Verdict: **partial / unwired**. Math correct; transition helper never invoked from the booted worker; HTTP retry hits the `updated_at` schema bug.

### Story 6.6 — Reaper

| AC | Status | Evidence |
|----|--------|----------|
| Reaper every `reaper_interval_sec`, the §6.6 UPDATE | complete | `db/jobs_reaper.py:53-73` matches (CTE form, paused_reason='crash', claimed_by=NULL); `Reaper._run` loop `reaper.py:144-162`; **started at boot** `runtime.py:293` |
| `stale_claim_sec=90 = 18×5s` | complete | `reaper.py:73-81` enforces ratio; `__main__.py:86` `stale_claim_sec = heartbeat_sec*18` |
| `NOTIFY jobs.reaped {id,prev_state,paused_at_sec}` | complete | `_emit_notify_pg:192-206`, SQLite publish `:181-188` |
| pg_advisory_lock single-runner | complete | `reaper._try_lock:90-109` `pg_try_advisory_lock`, key `0x6A6F6273` |
| never reaps terminal/pending/paused | complete | WHERE `state IN ('claimed','running','resuming')` `jobs_reaper.py:57,79` |

Verdict: **complete** and genuinely running at boot. Best-wired story after 6.1/6.2.

### Story 6.7 — Concurrency model & per-host caps

| AC | Status | Evidence |
|----|--------|----------|
| Load concurrency map; defaults §7.4 incl `subtitle_gen=2` | unwired | `concurrency.DEFAULT_CONCURRENCY:51-59` correct values, BUT `runtime.run()` builds `asyncio.Semaphore(cfg.stage_concurrency.get(stage,1))` (`runtime.py:263-265`) with `stage_concurrency` defaulting to **empty dict** → every stage cap = **1**. `ConcurrencyManager` never instantiated. No `pipeline.toml` loader feeds `RuntimeConfig`. |
| Per-stage semaphore acquire(timeout=0) before claim | partial | `ClaimLoop._stages_with_capacity:203-228` gates on semaphore value, but caps are all 1 (above) |
| Per-device GPU lock keyed by device_id | unwired | `concurrency.py:108-201` + `devices.py` exist; never used by runtime |
| `--stages` flag scopes claims | complete | `__main__.py:144-148` `_parse_stages`; `WorkerConfig.supported_stages` honored in claim |

Verdict: **unwired**. Defaults & GPU locks coded but the booted worker uses hardcoded cap=1 plain semaphores and no device serialization.

### Story 6.8 — Graceful shutdown

| AC | Status | Evidence |
|----|--------|----------|
| SIGTERM → shutdown event, stop claims, pause-request all held rows, wait grace, force-pause stragglers | unwired | `ShutdownOrchestrator` (`shutdown.py:101-253`) implements all 4 steps + 2nd-signal `os._exit(130)`, but `runtime.run()` uses only `install_signal_handlers` (`runner.py:256`) which just sets an Event — the loop drains in-flight tasks but **never sets `pause_requested` for held rows, never force-pauses, has no grace deadline**. `ShutdownOrchestrator` never instantiated in runtime. |
| Reaper backstops orphans | complete | reaper running (6.6) |
| Tests use real SIGTERM subprocess | n/a (test) | `tests/pipeline/test_shutdown.py` exercises orchestrator directly |

Verdict: **unwired**. On real SIGTERM, claimed rows are left `claimed` (claimed_by set) until the 90 s reaper sweep — the spec's clean-pause guarantee is not met by the boot path.

### Story 6.9 — Observability

| AC | Status | Evidence |
|----|--------|----------|
| `GET /api/queue/stats` returns §6.9 shape (`by_stage`,`by_state`,`eta_total_sec`,`realtime_factor_p50`) | partial/divergent | `jobs.go:334-457` returns different schema: `by_stage` w/ `done_24h` (not `done`), **no `by_state`**, `eta_sec` not `eta_total_sec`, **no `realtime_factor_p50`**, extra `workers[]`. Stage seed list uses `audio_extract/subtitle/embed/rehash` — **not the canonical enum** |
| Prometheus `/metrics` with `maktaba_jobs_total`, `maktaba_job_attempts_total`, `maktaba_job_duration_seconds`, `maktaba_job_realtime_factor` | unwired | `observability.py:291-389` implements all four + `render_prometheus_text`, BUT grep finds **zero references** to these metric names in `api/` or `shared/metrics/`; the worker never calls `record_attempt`/`set_jobs_count`; API `/metrics` (`main.go:187`) serves a different middleware registry |
| structlog `{job_id,video_id,stage,state,attempts}` per state event | unwired | `observability.log_event:469-498` enforces keys, but no state-transition helper or runner call site invokes it (grep: only its own docstring) |

Verdict: **unwired/divergent**. Metrics & log_event coded but not emitted; stats endpoint shape does not match spec.

### Story 6.10 — Single source of truth for resume

| AC | Status | Evidence |
|----|--------|----------|
| CHECK `last_segment_end_sec>=0 AND <= COALESCE(total_duration_seconds, ...)` | complete | `0002_processing_jobs.sql:88-92`; SQLite `:67-71` |
| Migration test: no `*_resume_offset` column elsewhere | complete | `tests/lint/test_no_resume_offset_columns.py:38` |
| Property test invariant across crash/resume | complete | `tests/property/test_resume_invariant.py:140` |
| Runner refuses sidecar checkpoint; lint enforces | complete | `tests/lint/test_no_sidecar_checkpoint.py:40` |

Verdict: **complete** (invariant + guard tests present).

## Top gaps by impact

1. **Worker dispatch is no-op placeholders (Stories 6.3–6.9 effectively
   dead at runtime).** `runtime.build_default_dispatch` →
   `_placeholder_handler` marks every job `done`
   (`runtime.py:218-235`); no production `dispatch_overrides` caller
   exists. Consequently `mark_failed_or_retry`, `should_pause/cancel`,
   `ForcePauseListener`, `ShutdownOrchestrator`, `ConcurrencyManager`,
   and all observability hooks — though correctly implemented — are
   never invoked by the booted daemon. The audit's "primitives exist
   but nothing instantiates them" is half-right: the loop+reaper ARE
   instantiated, but the *work* and the failure/control/shutdown
   layers are not.

2. **Go control endpoints write a nonexistent `updated_at` column.**
   Every pause/resume/cancel/retry UPDATE in
   `api/internal/handlers/jobs/jobs.go` sets `updated_at=$2`, but
   `processing_jobs` (migration `0002`, no later ALTER) has no such
   column — these statements fail on real Postgres. The entire HTTP
   control plane is broken against the production schema.

3. **No control-plane NOTIFYs from the API (Story 6.4).** None of
   `jobs.flag_set` (pause/resume/cancel) or `jobs.force_pause` are
   emitted by `jobs.go`. Even if the worker observed flags, force-pause
   abort signalling and WS fan-out for flag changes cannot work; the
   force-pause SQL also targets the wrong states and never records
   `paused_at_sec`, losing the resume offset.

4. **Graceful shutdown not wired (Story 6.8).** Boot path uses only an
   Event-setting signal handler; on SIGTERM, claimed rows are not
   pause-requested or force-paused and depend entirely on the 90 s
   reaper sweep, violating the clean-pause-within-grace guarantee.

5. **Concurrency caps hardcoded to 1 (Story 6.7).** `runtime.run()`
   ignores `DEFAULT_CONCURRENCY`, `ConcurrencyManager`, GPU device
   locks, and any `pipeline.toml`; every stage gets a cap-1 plain
   semaphore.

6. **`/api/queue/stats` shape diverges from spec (Story 6.9):** missing
   `by_state`, `realtime_factor_p50`; wrong `eta` key; non-canonical
   stage names (`audio_extract`, `subtitle`, `embed`, `rehash`).

## Status summary

- complete: 6.1, 6.2, 6.6, 6.10 (4)
- partial: 6.3, 6.5 (2)
- unwired: 6.7, 6.8, 6.9 (3)
- missing/broken: 6.4 (1)
