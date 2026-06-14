# Plan 26.7 — Background enrichment pipeline — implementation

> Implementation plan for [story-26-07-background-enrichment-pipeline.md](story-26-07-background-enrichment-pipeline.md).
> Self-contained. This is the **wiring** story: it registers the
> `classify` stage (running [Plan 26.1](plan-26-01-title-parser.md) +
> [26.2](plan-26-02-transcript-topic-extraction.md)), creates the
> out-of-band `enrich_jobs` queue (running
> [26.5](plan-26-05-web-metadata-enrichment.md)), and triggers the
> debounced library passes ([26.3](plan-26-03-series-detection.md) +
> [26.4](plan-26-04-auto-collection-builder.md)). It touches the
> orchestrator (`orchestrator/advance.py`), the runner
> (`pipeline/runner.py`), the state/stage enums (`domain/states.py`),
> and `processing_jobs`. Writes slot 0079.

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **`classify` is a real top-level stage** between `index` and `thumbnail`, with a `CLASSIFIED` `videos.state`. Local, fast, deterministic-ish ⇒ safe on the critical path. | Story stage placement. It needs the unit embeddings `index` wrote, so it must follow `index`. |
| D2 | **`enrich` is NOT a stage and NOT a state.** It's a separate `enrich_jobs` table + worker, decoupled from `videos.state`. | Story: networked/rate-limited work must never block `READY`. |
| D3 | **`classify` failure is isolated** (log + metric, video still advances), exactly like chapter inference (Plan 5.7 D8). | Classification is an enhancement, not a gate. |
| D4 | **Enrich enqueued on `classify` done**, gated by `settings.enrich.enabled` + ≥1 provider key; ordering guarantee: no enrich without a completed classify. | Story AC. |
| D5 | **Group passes (series + collections) are debounced + coalesced at the library level.** A per-library timer (default 30 s) resets on each `classify`/`enrich` completion; firing runs both passes once. | Story AC: O(batches) not O(videos); avoids partial groupings. |
| D6 | **Re-classify on transcript change or version bump.** A small reconciler enqueues `classify` for videos whose `parser_version`/`model_version`/active-transcript is stale. | Story AC: keep classifications current without manual action. |
| D7 | **Backfill path** for enabling classify/enrich on an existing library enqueues jobs for `READY` videos without rerunning earlier stages. | Story AC. |

If D2 is rejected (enrich as a stage): a rate-limited TMDb key or an
offline box would strand videos out of `READY` — the exact failure the
story forbids. Rejected.

---

## 1. Stage & state registration

`pipeline/.../domain/states.py`:

```python
class VideoState(StrEnum):
    ...
    INDEXED = "indexed"
    CLASSIFIED = "classified"     # NEW (after INDEXED, before THUMBNAILED)
    THUMBNAILED = "thumbnailed"
    ...

class Stage(StrEnum):
    SCAN = "scan"; AUDIO = "audio"; TRANSCRIBE = "transcribe"
    SUBTITLE_GEN = "subtitle_gen"; INDEX = "index"
    CLASSIFY = "classify"          # NEW
    THUMBNAIL = "thumbnail"
```

`orchestrator/advance.py` (the single sanctioned state mutator) gains
the `INDEXED → CLASSIFIED → THUMBNAILED` transitions and the rule that
`classify` failure advances to `THUMBNAILED` anyway (D3). The transition
table and any state diagram in
[Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md)
docs are updated to the eight-state path.

## 2. The `classify` stage handler

```
pipeline/src/maktaba_pipeline/pipeline/stages/classify.py
```

```python
async def run_classify_stage(ctx, job):
    video_id = job["video_id"]
    try:
        parsed = title.parse(filename(video_id), dirnames=dirs(video_id))   # 26.1
        await classify_repo.write_parsed_title(ctx.conn, video_id, parsed)
        result = await classifier.classify(ctx, video_id)                    # 26.2
        await classify_repo.write_classification(ctx.conn, video_id, result)
    except Exception as e:                                                    # D3
        log.warning("classify_failed", video_id=video_id, error=str(e))
        await record_metric(ctx.conn, video_id, "classify_failed", e)
        # do NOT re-raise; advance_after_stage still moves to thumbnail
    await maybe_enqueue_enrich(ctx, video_id)                                 # D4
    await schedule_group_passes(ctx, library_of(video_id))                   # D5
```

`maybe_enqueue_enrich` checks `settings.enrich.enabled` and that at least
one provider adapter `configured(secrets)` before inserting an
`enrich_jobs` row (D4).

## 3. The `enrich` worker (out-of-band, D2)

```
pipeline/src/maktaba_pipeline/enrich/worker.py
```

A worker loop modeled on the existing `pipeline/runner.py` claim
pattern but over `enrich_jobs`, decoupled from `processing_jobs`:

```python
async def enrich_worker(ctx):
    while running:
        job = await claim_enrich_job(ctx.conn)        # SKIP LOCKED, backoff-aware
        if job is None:
            await sleep_or_wakeup(); continue
        try:
            res = await enrich_service.enrich_video(ctx.conn, secrets, job.video_id,
                                                    force=job.force)   # 26.5
            await complete_enrich_job(ctx.conn, job, res)
            await maybe_auto_accept(ctx, job.video_id)  # 26.6 threshold (D, opt-in)
            await schedule_group_passes(ctx, library_of(job.video_id))  # series desc may need refresh
        except ProviderPaused:                          # breaker open
            await defer_enrich_job(ctx.conn, job, reason="provider_paused")
        except RateLimited:
            await defer_enrich_job(ctx.conn, job, reason="rate_limited")  # next window
        except Exception as e:
            await retry_or_fail_enrich_job(ctx.conn, job, e)   # backoff; never touches videos.state
```

`defer_*` reschedules without consuming an attempt (provider-paused /
rate-limited are not the job's fault).

## 4. Debounced group passes (D5)

```
pipeline/src/maktaba_pipeline/classify/group_scheduler.py
```

A per-library debounce keyed in a small in-memory map plus a persisted
`library_group_pending` marker (so a restart still runs a pending pass):

```python
async def schedule_group_passes(ctx, library_id):
    await mark_group_pending(ctx.conn, library_id)        # persisted (survives restart)
    debounce.reset(library_id, GROUP_DEBOUNCE_SEC, lambda: run_group(ctx, library_id))

async def run_group(ctx, library_id):
    if not await take_group_pending(ctx.conn, library_id): return  # coalesced
    await series.run_series_detect(ctx.conn, library_id, ...)      # 26.3
    await collections.run_auto_collections(ctx.conn, library_id)  # 26.4
```

On-demand trigger: a maintenance endpoint / admin action calls
`run_group` directly (and a "re-enrich all" enqueues enrich jobs for the
library's `READY` videos).

## 5. Data model — migration slot 0079

```sql
-- Slot 0079 (Epic 26 / Story 26.7)
-- Postgres state/stage enums are TEXT with CHECKs in this codebase; widen them.
-- (videos.state and processing_jobs.stage are TEXT — see slot 0004.)

ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_state_check;
ALTER TABLE videos ADD CONSTRAINT videos_state_check
    CHECK (state IN (... existing ..., 'classified'));

CREATE TABLE IF NOT EXISTS enrich_jobs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id    UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','running','done','deferred','failed')),
    force       BOOLEAN NOT NULL DEFAULT false,
    attempts    INTEGER NOT NULL DEFAULT 0,
    not_before  TIMESTAMPTZ NOT NULL DEFAULT now(),   -- backoff / defer window
    last_error  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (video_id, status) DEFERRABLE INITIALLY DEFERRED  -- at most one open job/video
);

CREATE TABLE IF NOT EXISTS library_group_pending (
    library_id  UUID PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    marked_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS enrich_jobs_claim_idx
    ON enrich_jobs (status, not_before) WHERE status IN ('pending','deferred');
```

`processing_jobs.stage` is TEXT (slot 0002/0004); the `classify` value
needs no schema change beyond any CHECK widening (mirror the videos.state
CHECK pattern if a stage CHECK exists).

## 6. Resumability

- Crash mid-`classify`: the `processing_jobs` row is reclaimed (existing
  reaper, `pipeline/.../pipeline/reaper.py`) and re-run; writes are
  idempotent (parser/classification upsert + atomic replace).
- Crash mid-`enrich`: the `enrich_jobs` row is reclaimed; `enrich_video`
  is idempotent via `external_id` + cache.
- Crash with a pending group pass: `library_group_pending` survives;
  startup drains pending passes.

## 7. Files to create / modify

**Create:** `pipeline/.../pipeline/stages/classify.py`,
`pipeline/.../enrich/worker.py`, `classify/group_scheduler.py`, migration
pair.

**Modify:**
- `pipeline/.../domain/states.py` — add `CLASSIFIED` + `CLASSIFY`.
- `pipeline/.../orchestrator/advance.py` — new transitions + failure
  advance (D3).
- `pipeline/runner.py` — register the `classify` stage; start the enrich
  worker + group scheduler.
- `pipeline/.../pipeline/reaper.py` — reclaim enrich jobs.
- `pipeline/.../pipeline/wakeup.py` — wake the enrich worker on enqueue.
- `specs/architecture.md` §7 + Epic 1 Story 1.6 stage docs — eight-state
  path.
- `MANIFEST.md` — slot 0079 (+ note: cross-cutting `videos.state` /
  `processing_jobs.stage` change, like `processing_jobs`/`chapters`
  ownership notes already in the manifest).

## 8. Dependencies

- **26.1, 26.2** (classify body), **26.5** (enrich body), **26.3, 26.4**
  (group passes), **26.6** (auto-accept hook).
- **Epic 1** state machine, **Epic 6** job queue/runner/reaper.

## 9. API contract

```
POST /api/videos/{id}/enrichment/reenrich       → enqueue enrich_job(force by id)
POST /api/libraries/{id}/reenrich               → bulk enqueue for READY videos
POST /api/libraries/{id}/reclassify             → enqueue classify backfill (D7)
POST /api/libraries/{id}/regroup                → run group passes on demand (D5)
```

## 10. Test strategy

End-to-end stage test: a fresh video traverses `…index→classify→
thumbnail→READY` with `CLASSIFIED` observed; fault-injected classify
still reaches READY. Enrich decoupling test stalls the enrich worker and
asserts READY. Debounce test imports N videos and asserts exactly one
series + one collection pass. Resume tests kill the worker mid-classify
and mid-enrich. Ordering test asserts no enrich precedes its classify.
Backfill test enables classify on a `READY`-only library and asserts
classify jobs (not earlier stages) are enqueued.
