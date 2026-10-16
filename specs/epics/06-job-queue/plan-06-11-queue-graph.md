# Plan 6.11 — Queue graph engine and control centre (contract)

> **Status:** contract for implementation. No production code in this document.
> **Branch:** `feat/queue-engine`.
> **Extends:** Epic 6 (`processing_jobs`, claim loop, heartbeat, reaper, backoff).
> **Aligns with:**
> - [architecture §7](../../architecture.md#7-batch-processing)
> - [plan-26-07](../26-content-intelligence/plan-26-07-background-enrichment-pipeline.md) (debounced library passes)
> - [Epic 27](../27-live-channels/README.md) (channels)
> - [plan-11-05](../11-web-ui/plan-11-05-processing-queue-dashboard.md) (queue page)
> - [plan-21-07](../21-observability/plan-21-07-job-pipeline-visibility.md) (pipeline visibility)
>
> **Replaces:** the hand-chained orchestration in `pipeline/scripts/mediaintel/pipeline.py` (`_run`, lines 57-196).

The user asked for this, in their words:

- "When one job finishes an episode's tagging, thumb, audio, whatever, it pushes to the next step's queue, and that starts on it, and so on."
- "When an episode is updated, it and its season are sent to the next step to update their info, and update whatever updates with them."
- "Retrigger for a library just re-adds it to the right queue."
- They also want a control centre with a gauge, logs, progress and issues per queue, in light and dark themes.

This plan does that by **extending `processing_jobs`**. It does not build a second queue.

---

## 0. Verified current state (what this plan builds on and fixes)

Every row below was checked against the worktree at commit `50c430a`.

| # | Fact | Evidence |
|---|------|----------|
| F1 | `processing_jobs` has no `updated_at`, version, input-hash, output-hash or dependency columns. | `shared/db/migrations/0002_processing_jobs.sql:32-93` |
| F2 | One live row per `(video_id, stage)` is enforced by a partial UNIQUE over `pending, claimed, running, resuming, paused`. | `0002_processing_jobs.sql:124-126` |
| F3 | An AFTER INSERT trigger sends `pg_notify('jobs.new', {id, video_id, stage, priority})`. | `0002_processing_jobs.sql:129-150` |
| F4 | Slot 0058 made `video_id` nullable, added `library_id`, `processing_jobs_scope_chk` (scan rows are library-scoped, all other rows video-scoped) and `processing_jobs_one_live_scan_per_library`. | `0058_processing_jobs_library_scoped.sql:44-91` |
| F5 | The SQLite twin of 0058 keeps `video_id NOT NULL` and has no scope CHECK. | `0058_processing_jobs_library_scoped.sqlite.sql:14-34` |
| F6 | **BUG.** Postgres carries three stage CHECKs: `processing_jobs_stage_chk` (0002), `processing_jobs_stage_valid` (0004) and `processing_jobs_stage_check` (0049). Slot 0049 drops a constraint that never existed (`processing_jobs_stage_check`) and adds a wider one, so the two narrow ones still reject `topic_recluster`, `topic_assign`, `categorize` and `chapter_infer`. Any new stage fails the same way. SQLite is not affected, because its 0049 twin rebuilt the table. | `0002:68-73`, `0004_video_states_and_stages.sql:72-91`, `0049_chapter_infer_stage.sql:18-27`, `0049_…sqlite.sql:14-89` |
| F7 | **BUG.** All eight Go control UPDATEs (pause, force-pause, resume, cancel, retry and the three per-video variants) set `updated_at`. That column does not exist, so each returns 500 on Postgres. | `api/internal/handlers/jobs/jobs.go:157,162,199,225,270,288,306,323` |
| F8 | **BUG.** Nothing publishes to the SSE channel `jobs`. `Bus.Publish` has no production caller, no Go code LISTENs on `jobs.new` or `jobs.progress`, and no Python code writes the `events` table. `/ws/jobs` only ever sends `: ping`. | `api/internal/eventbus/bus.go:102` (no callers), `api/internal/router/p6_eventbus.go:26-42` (bus is local, never handed to handlers), `pipeline/src` (no `INSERT INTO events`) |
| F9 | **BUG.** The SSE writer never emits `id:` lines, so the browser never sends `Last-Event-ID` and the replay path (`parseLastEventID`, `busReplayAdapter`) is dead in practice. | `api/internal/handlers/ws/ws.go:221-224, 234-247`, `p6_eventbus.go:47-64` |
| F10 | **BUG.** `RuntimeConfig.stage_concurrency` is never filled in, so every stage runs with `Semaphore(1)`. `pipeline/concurrency.py` (`DEFAULT_CONCURRENCY`, `GPU_STAGES`) is unused. Caps are also per process, so two workers on one host double the NAS load. | `runtime.py:77, 263-265`, `__main__.py` `_build_runtime_config` (no `stage_concurrency`), `pipeline/concurrency.py:51,65` |
| F11 | **BUG.** `GET /api/queue/stats` hard-codes stage names that do not exist (`audio_extract`, `subtitle`, `embed`, `rehash`). It reports one "worker" per running job, and `eta_sec` is always 0. | `jobs.go:335-431` (stage list at :366) |
| F12 | `Job.ID` is a Go `string` although the column is BIGSERIAL. `error` and `estimated_remaining_sec` are never selected. | `jobs.go:35-51, 461-505` |
| F13 | Stages are chained by hand: scan→probe (`scanner/store.py:230`), probe→extract (`audio/probe.py:350`), extract→transcribe (`audio/extract.py:504`), transcribe→subtitle_gen+index (`stt/transcribe.py:391`), index→thumbnail (`search/index_stage.py:295`). | as cited |
| F14 | There are two `Stage` enums: `db/jobs.py:64-78` and `domain/states.py:67-83`. Neither lists the 0049 stages. Dispatch map: `handlers.py:1010-1016`. A stage without a handler is failed non-retryably (`runtime.py:200-209`). | as cited |
| F15 | Failure errors are stored as JSON text `{kind, message, traceback, retryable}`. Backoff is 60 s × 2^(n-1), capped at 3600 s, with ±25 % jitter. | `db/jobs_state.py:271-330`, `pipeline/backoff.py` |
| F16 | Heartbeat is 5 s (`pipeline/heartbeat.py:38`). The stale-claim threshold is 90 s = 18 × heartbeat (`pipeline/reaper.py:18`). | as cited |
| F17 | `enrich_jobs` and `library_group_pending` (slot 0079) form a separate queue. The `GroupScheduler` 30 s debounce is only referenced by `classify_stage.run_classify`, which no dispatch map calls. | `0079_classify_enrich_jobs.sql:21-40`, `classify/group_scheduler.py:58`, `classify/classify_stage.py:29,99` |
| F18 | SSE envelope is `{type, at, payload}`. Routes: `/ws/jobs` (channel `jobs`), `/ws/library/{id}`, `/ws/playback/{video_id}`. Cross-replica fan-out goes through the `events` table + `pg_notify('ws.events')`, with 7-day retention and a 500-row replay cap. | `ws.go:36-40,144-154`, `0061_events.sql:53-98`, `eventbus/bus.go:15,105,122-215` |
| F19 | The mediaintel prototype keeps results in `index_jobs (path, stage)` with `version, size, mtime, state ∈ {done, failed}`. Its stage versions are `DHASH_VERSION`, `SIG_VERSION = AUDIO_FP_VERSION*100 + FRAMES_VERSION` and `STYLE_VERSION`. | `scripts/mediaintel/jobs.py:25-113`, `signatures.py:45` |
| F20 | Two-pass fingerprinting: `PER_SEASON=4` samples are taken only from seasons that have unplaced candidates (`LENGTH_BAND=0.35`, `DATE_SLACK_DAYS=180`). Candidates are read only when `has_signature(samples)` (≥ half the pairs match). NAS reads are capped at 2. The detector opens the date window for `ongoing` shows. | `signatures.py:42-199`, `classify/series_detector.py:498-513` |
| F21 | Analysis versions: `DETECTOR_VERSION=2`, `TAGGER_VERSION=1`, `STYLE_VERSION=1`, `PLANNER_VERSION=1`, `DHASH_VERSION=1`, `FRAMES_VERSION=1`, `AUDIO_FP_VERSION=1`. | `series_detector.py:85`, `topic_tags.py:95`, `production_style.py:77`, `channel_planner.py:85`, `dhash.py:27,67`, `chromaprint.py:38` |
| F22 | Maktaba's `series` is one row per **series**, and `series_episodes` carries `season`. No season table exists. The mediaintel `series` table is one row per **season**. | `0075_series.sql`, `scripts/mediaintel/db.py:96-113` |
| F23 | Web theming:<br>• `<html data-theme>`, controlled by `src/lib/theme.ts` with no-flash boot in `index.html:13-29`.<br>• App tokens live in `src/styles/tokens.css`.<br>• **BUG:** dark mode does not override `--color-success/warn/error` (`tokens.css:61-71`).<br>• **BUG:** design-system CSS reads `--color-semantic-*`, which is never defined, so DS components fall back to hard-coded light colours in dark mode (`design-system/components/Badge/badge.css:4-32`). | as cited |
| F24 | **BUG.** `web/src/lib/ws.ts` opens a new `EventSource` for every `subscribe` call, although its comment says streams are shared (`ws.ts:96-102`). `VideoDetail.tsx:284` subscribes to `library/${videoId}`, passing a video id as a library id. | as cited |
| F25 | The scanner hashes only the head and tail of each file (`identity/hasher.py:107-130`, `head_tail`), so Maktaba can scan NAS libraries cheaply. That is a precondition for importing mediaintel results (§10.4). | as cited |

Bugs F6, F7, F8, F9, F10, F11, F23 and F24 are fixed by this plan:

| Bug | Fixed in |
|-----|----------|
| F6 | §10.1 (A) |
| F7 | §8.9 (B) |
| F8 | §7 (A + B) |
| F9 | §7.4 (B) |
| F10 | §5 (A) |
| F11 | §8 (B) |
| F23 | §9.1 (C) |
| F24 | §9.4 (C) |

---

## 1. Decisions

| # | Decision | Why |
|---|----------|-----|
| D1 | **Jobs act on a subject:** `(subject_kind, subject_id)`. `subject_kind ∈ {video, library, series, season, catalog}`. Existing typed FK columns are kept (`video_id`, `library_id`) and two more are added (`series_id`, `season_id`), so `ON DELETE CASCADE` still removes a deleted thing's jobs. `subject_id` duplicates whichever typed column is set. A CHECK pins the pairing. | Reusing `video_id`/`library_id` alone cannot express season or catalog subjects. A bare polymorphic `subject_id` would lose cascades. New columns plus a backfill are append-only and metadata-only DDL (§10.2). |
| D2 | **`catalog`** replaces the brief's word "collection" for the single global planning subject. Its `subject_id` is the fixed UUID `00000000-0000-0000-0000-00000000ca7a`. | "collection" already means Maktaba `collections` (slot 0076) and Plex collections, which are a stage output here. The name would be ambiguous in logs and UI. |
| D3 | **The stage graph is a Python module** (`maktaba_pipeline/engine/graph.py`). At start-up each worker upserts the graph into a new table `job_stages`. Go and the web read stages only from that table. The table also replaces every stage CHECK with a foreign key. | One source of truth. Adding a stage stops needing a CHECK migration, which removes the F6 bug class for good. |
| D4 | **Change-driven propagation.** Each job records `stage_version`, `input_hash` and `output_hash`. A successful job whose output hash differs from the previous successful run of the same subject and stage enqueues its graph dependents, in the **same transaction** as `done`. An unchanged output enqueues nothing. | This is the user's "pushes to the next step" with no wasted work. Doing it in the same transaction means no lost or phantom triggers. |
| D5 | **Skip checks run in the worker after claim, not at enqueue.** Each stage defines `input_fingerprint(db, subject)`, a DB-only query that never reads the NAS. If version and input hash equal the last success and the job is not forced, the job finishes as `skipped_unchanged` in milliseconds. | One implementation, in Python. Go's retrigger never computes hashes. Re-adding a whole library costs one cheap row per subject. |
| D6 | **One live row per `(subject_kind, subject_id, stage)`, the same invariant as today.**<br>• A new trigger on a **pending** row coalesces: lowest priority wins, `force` is ORed, reasons are merged, the debounce is extended.<br>• A trigger on a **claimed/running/resuming/paused** row sets `rerun_requested`. When that run ends, the engine inserts exactly one follow-up pending row in the same transaction. | Changes that arrive during a run are not lost, and the existing unique-index idempotency (F2) keeps working unchanged. A two-index "pending + active" scheme would force claim-time `NOT EXISTS` checks and would make the Go retry path conflict. |
| D7 | **Debounce** applies to library, season and catalog stages that set `debounce_sec`. On every coalesce, `not_before = LEAST(now() + debounce_sec, created_at + debounce_max_sec)`. | This is the plan-26-07 D5 semantics, persisted in the job row itself. It supersedes `library_group_pending` for the new stages. That table stays for the legacy classify path. |
| D8 | **Enqueue is one SQL function**, `processing_jobs_enqueue(...)`, on Postgres. Python and Go both call it. On SQLite, Python carries an equivalent implementation, held to the same tests. | Two languages need byte-identical coalescing rules. Go runs on Postgres only (verified: `api/main.go:323` opens only `postgres`). |
| D9 | **Worker pools are resource classes stored in a table**, `worker_pools`. `scope='global'` pools (`nas_read`, `network_plex`, `network_tv`, `network_yt`) are enforced **in the claim SQL** across all processes, under a per-pool advisory lock. `scope='per_worker'` pools (`cpu`, `gpu`) are enforced by in-process semaphores. Workers re-read sizes at the start of every drain and on `pg_notify('jobs.control')`. | The NAS is shared by every worker. F10 shows that per-process caps cannot protect it. CPU and GPU are per host. |
| D10 | **Pausing a queue stops claims; it does not interrupt running jobs.** `POST …/pause?interrupt=true` also sets `pause_requested` on running rows of stages that support cooperative pause (only `transcribe` today). Global pause is the singleton `queue_control.paused`. | Short stages finish in seconds. Killing them mid-write gains nothing. |
| D11 | **Stages that write to external systems** (`plex_collection`, `dizquetv_show`, `write_channels`) have `write_mode ∈ {apply, review}`, default `review`. In review mode the job computes and stores the diff and finishes with outcome `awaiting_review`. That shows as a "Needs attention" issue whose one action is **Apply**. | The user's standing rule: "Before any write, show the user a dry-run diff." Review mode makes the queue honour it. They flip a queue to `apply` once they trust it. |
| D12 | **Fingerprints follow the mediaintel two-pass method, expressed as jobs:**<br>1. `detect` requests sample fingerprints.<br>2. A season-level `fp_gate` decides whether candidates are worth reading and requests them.<br>3. Candidate fingerprints that change re-trigger `detect`.<br>Termination is argued in §3.4. | The method protects the NAS (F20). As jobs, each step is resumable and visible in the UI. |
| D13 | **Logs live in `job_events`.** It is append-only, has one row per meaningful event per job, and is partitioned by retention class. **Issues** are open failures grouped by `(stage, error_cause)`. `error_cause` is a normalized code written at failure time (§6.3). | Issues need cheap GROUP BY and stable grouping across message noise. Paths, numbers and ids must not split groups. |
| D14 | **Live updates:**<br>• Python inserts lifecycle rows into the existing `events` table (channel `jobs`) inside the job's own transaction.<br>• The existing bus fans them out across replicas.<br>• Go publishes an ephemeral `queues.snapshot` every 2 s, straight to the hub, on a new channel `queues` (`/ws/queues`), only while it has subscribers. | This reuses the existing hub, replay and fan-out path (F18) and fixes F8. Stats snapshots need no durability or replay. |
| D15 | **Existing stages keep their handlers and their manual chaining** (F13) and are marked `legacy=true` in the graph. The engine still records their output hash after success and propagates their declared non-legacy edges, e.g. `probe → thumb_hash, link, detect`. | Nothing that works today changes behaviour. The new lanes attach to `probe`'s output. |
| D16 | **Analysis results get first-class tables** (§4.4), not `videos.metadata`. Chromaprints of two 90 s windows run to several KB per video. They would bloat every `SELECT * FROM videos` on the hot library-browse path. | Plan-26-03 D5 suggested `videos.metadata.poster_dhash`. A small hash could live there, but fingerprints and style metrics should not. One table, `video_signals`, keeps all per-video signals together. |

---

## 2. Overview

```
            ┌───────────────────── engine/graph.py (single source of truth) ─────────────────────┐
            │  StageDef(name, subject_kinds, pool, version, input_fingerprint, run, edges, …)     │
            └──────────┬───────────────────────────────────────────────────────────┬──────────┘
                 sync on worker start                                     run / propagate
                       ▼                                                           ▼
  ┌──────────────┐  job_stages  worker_pools  queue_control        processing_jobs (subject, stage, hashes)
  │  Go API (B)  │◄─────────────── read ────────────────┐                 │  ▲
  │  /api/queues │──── processing_jobs_enqueue(…) ──────┼────────────────►│  │ claim_for_pool (advisory lock / pool)
  │  /ws/queues  │──── UPDATE job_stages/pools ─────────┘  NOTIFY jobs.control   │
  └──────┬───────┘                                                        │  │
         │ hub.Publish("queues", snapshot) every 2 s                      ▼  │
         │                         events (channel 'jobs') ◄── same tx ── Python worker (A)
         │                                │ pg_notify('ws.events')       job_events, health_samples
         ▼                                ▼
     Web (C): /queue  /queue/:stage  /queue/items/:kind/:id   ← EventSource /ws/jobs, /ws/queues
```

---

## 3. The stage graph

### 3.1 Python shape (`pipeline/src/maktaba_pipeline/engine/graph.py`)

```python
SubjectKind = Literal["video", "library", "series", "season", "catalog"]
CATALOG_ID = UUID("00000000-0000-0000-0000-00000000ca7a")

@dataclass(frozen=True, slots=True)
class Edge:
    to: str                                   # target stage name
    map: Literal["same", "library_of", "season_of", "seasons_changed",
                 "videos_of", "catalog"]      # subject mapping, see §3.3
    when: Literal["output_changed", "always"] = "output_changed"
    legacy: bool = False                      # display-only (handler enqueues it itself)

@dataclass(frozen=True, slots=True)
class StageDef:
    name: str                                 # == processing_jobs.stage
    label: str                                # UI card title (i18n key falls back to it)
    subject_kinds: tuple[SubjectKind, ...]
    pool: str                                 # worker_pools.pool
    lane: Literal["sources", "episodes", "seasons", "channels", "media"]
    lane_order: int
    version: int                              # bump ⇒ every subject re-runs once
    run: StageHandler | None                  # None ⇒ legacy handler from handlers.py
    input_fingerprint: Callable[[DBConn, Subject], Awaitable[str | None]] | None
    output_fingerprint: Callable[[DBConn, Subject], Awaitable[str]] | None  # legacy stages only
    edges: tuple[Edge, ...] = ()
    may_request: tuple[str, ...] = ()         # stages run() may enqueue explicitly (StageResult.requests)
    entry_for: tuple[SubjectKind, ...] = ()   # "re-queue <kind>" starts here
    debounce_sec: int = 0
    debounce_max_sec: int = 0
    external_write: bool = False              # D11: has write_mode
    legacy: bool = False
    hidden: bool = False                      # shown as a sub-step, not its own card
    max_attempts: int = 3

@dataclass(frozen=True, slots=True)
class StageResult:
    output_hash: str                          # canonical_hash(persisted result)
    outcome: Literal["changed", "unchanged", "awaiting_review", "waiting_inputs"] | None = None  # engine infers changed/unchanged
    changed_subjects: tuple[tuple[SubjectKind, UUID], ...] = ()   # for map="seasons_changed"
    requests: tuple[Request, ...] = ()        # explicit enqueues, target ∈ may_request
    summary: dict[str, Any] = field(default_factory=dict)          # → processing_jobs.output_summary
    log: tuple[tuple[str, str, dict[str, Any]], ...] = ()          # (level, message, data) → job_events

@dataclass(frozen=True, slots=True)
class Request:
    stage: str
    subject_kind: SubjectKind
    subject_id: UUID
    reason: str                               # e.g. "sample of season <id>"
    payload: dict[str, Any] | None = None     # e.g. {"role": "sample", "seasons": ["…"]}
    priority: int | None = None
```

`canonical_hash(obj) = sha256(json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()).hexdigest()[:32]`

Python owns this function. Go never computes hashes.

`graph.validate()` runs in a unit test and at worker start. It checks that:

- every `edge.to` and every `may_request` entry is a defined stage;
- every `pool` exists;
- the edge relation without `legacy` edges is acyclic;
- `requests` cycles go only through stages whose input fingerprint is monotone (§3.4).

### 3.2 Stage table

Pools are defined in §5.

| Stage | Label (card) | Subject | Pool | Lane / order | Version source | Input fingerprint (DB only) | Output (persisted → hashed) | Edges (on output change) | May request | Debounce s (max) |
|---|---|---|---|---|---|---|---|---|---|---|
| `scan` (legacy) | Scan folders | library | nas_read | media/0 | 1 | none | output_fingerprint: count + hash of `(id, size_bytes, mtime)` of the library's videos | `probe`/videos_of (legacy) | – | 0 |
| `probe` (legacy) | Length | video | nas_read | episodes/1 | 1 | `size_bytes,mtime` | output_fingerprint: `duration_sec, metadata.width/height` | `thumb_hash`/same, `link`/same, `detect`/library_of; `extract`/same (legacy) | – | 0 |
| `thumb_hash` | Thumbnail | video | nas_read | episodes/2 | `DHASH_VERSION` | sidecar thumbnail (`<stem>.jpg/.webp/.png`) path+size+mtime, else `poster_path` | `video_signals.thumb_dhash` | `detect`/library_of | – | 0 |
| `link` | Link | video | cpu | episodes/3 | `LINK_VERSION=1` (new, `engine/stages/link.py`) | video `filename,title,duration_sec` + library's latest `youtube_sync.output_hash` | `media_metadata_enrichment` row `provider='youtube'` (external_id, link_method) | `detect`/library_of | – | 0 |
| `fingerprint` | Fingerprint | video | nas_read | episodes/4 | `SIG_VERSION` | `size_bytes,mtime,duration_sec` | `video_signals.audio_head/audio_tail/frame_hashes` (+`fp_partial`) | `detect`/library_of; `fp_gate`/payload seasons (see §3.3) | – | 0 |
| `style_sample` | Style sample | video | nas_read | episodes/5 | `STYLE_VERSION` | `size_bytes,mtime` | `video_signals.style_metrics` | `rate_style`/payload seasons | – | 0 |
| `youtube_sync` | YouTube listing | library | network_yt | sources/0 | `YT_SYNC_VERSION=1` | none (external; always runs) | hash of `youtube_playlists` + `youtube_playlist_items` + `youtube_videos` for the library's channels | `link`/videos_of, `detect`/same→library | – | 300 (3600) |
| `detect` | Place episodes | library | cpu | seasons/1 | `DETECTOR_VERSION` | md5 over sorted `(video_id, latest done output_hash of probe, thumb_hash, link, fingerprint)` + library `youtube_sync` output + style/season overrides | `series`, `series_seasons`, `series_episodes`; per-season `membership_hash` | `tag`/seasons_changed, `rate_style`/seasons_changed, `plex_collection`/seasons_changed, `dizquetv_show`/seasons_changed, `tag`/same (library: loose videos), `plan_channels`/catalog | `fingerprint`, `fp_gate` | 30 (300) |
| `fp_gate` (hidden) | Samples → candidates | season | cpu | seasons/1 | `SIG_VERSION` | sample set + their fingerprint output hashes + candidate set (from `series_seasons.metadata.fp_plan`) | `series_seasons.metadata.signature ∈ {shared, none, incomplete}` | – | `fingerprint` | 10 (120) |
| `tag` | Tag topics | season, library | cpu | seasons/2 | `TAGGER_VERSION` | season: `membership_hash`; library: hash of loose/short video titles | `season_tags`; `video_tags` (loose) | `plan_channels`/catalog | – | 0 |
| `rate_style` | Rate style | season | cpu | seasons/3 | `STYLE_VERSION` | `membership_hash` + sampled videos' `style_sample` output hashes + override | `season_style` | `plan_channels`/catalog | `style_sample` | 10 (120) |
| `plex_collection` | Plex collection | season | network_plex | seasons/4 | `PLEX_EXPORT_VERSION=1` | ordered member Plex rating keys + collection title + `write_mode` | `external_links(target='plex_collection')` | `dizquetv_show`/same | – | 0 |
| `dizquetv_show` | dizqueTV show | season | network_tv | seasons/5 | `DZ_SHOW_VERSION=1` | ordered members + Plex collection output hash + `write_mode` | `external_links(target='dizquetv_show')` | – | – | 0 |
| `plan_channels` | Plan channels | catalog | cpu | channels/1 | `PLANNER_VERSION` | md5 over all latest done output hashes of `detect`, `tag`, `rate_style` + planner config hash | `channel_plans` row (latest) | `write_channels`/same | – | 60 (900) |
| `write_channels` | Write channels | catalog | network_tv | channels/2 | `CH_WRITE_VERSION=1` | `channel_plans.input_hash` of latest plan + `write_mode` | `external_links(target ∈ maktaba_channel, dizquetv_channel)`; Maktaba `channels` rows + `channel_schedule_state.stale=true` | – | – | 0 |
| `extract`, `transcribe`, `subtitle_gen`, `index`, `thumbnail` (legacy) | as today | video | nas_read, gpu, cpu, cpu, nas_read | media/1..5 | 1 | none | output_fingerprint = finished_at (always "changed") | legacy chain (F13) | – | 0 |
| `topic_recluster`, `topic_assign`, `categorize`, `chapter_infer` (legacy, no handler) | as named | video / library | cpu | media/6..9 | 1 | none | – | – | – | 0 |

Notes on the table:

- **Entry stages** (`entry_for`):
  - video: `probe`
  - library: `youtube_sync`, `detect`
  - season: `tag`, `rate_style`, `plex_collection`
  - catalog: `plan_channels`
  - series: expands to its seasons (series is a display and history subject only; no stage runs on it in v1)
- **Display mapping to the approved layout:**
  - Episodes lane: length = `probe`, then `thumb_hash`, `link`, `fingerprint`, `style_sample`.
  - Seasons & libraries lane: `detect`, `tag`, `rate_style`, `plex_collection`, `dizquetv_show`.
  - Channels lane: `plan_channels`, `write_channels`.
  - `youtube_sync` and the `media` lane were not in the approved mock-up. They render as a compact "Sources" chip row above the lanes and a collapsed "Media processing" lane below them (open question Q1).
- **`write_channels` has two sub-steps**, `maktaba` (local DB, primary per the user's goal) and `dizquetv` (secondary). Each records its own output in `external_links`.
- **Missing integrations:** if Plex or dizqueTV is not configured (`MAKTABA_PLEX_URL`, `MAKTABA_PLEX_TOKEN`, `MAKTABA_DIZQUETV_URL`, `MAKTABA_PATH_MAP` JSON), the worker syncs the stage with `enabled=false` and `disabled_reason='not_configured'`. The enqueue function returns `outcome='disabled'` and inserts nothing.

### 3.3 Subject mapping

| `map` | From | To | Rule |
|---|---|---|---|
| `same` | any | same subject | – |
| `library_of` | video | library | `videos.library_id` |
| `season_of` | video | season | `series_episodes` → `series_seasons.id` (0 or 1 row) |
| `seasons_changed` | library (detect) | season | `StageResult.changed_subjects` (seasons whose `membership_hash` changed, plus seasons retired this run) |
| `videos_of` | library | video | All the library's videos for `link`. For legacy `probe` it is the scanner's own enqueue. `youtube_sync → link` passes only videos with no link, or whose YouTube id appears in the changed listing rows (`StageResult.changed_subjects`). |
| `catalog` | any | catalog | `CATALOG_ID` |
| payload seasons | video (`fingerprint`, `style_sample`) | season | `job.payload.seasons` (set by the requester); empty means no edge |

### 3.4 Fingerprint two-pass flow and why it terminates

1. **`detect(L)`** runs the detector with whatever signals exist. For each season *S* that has unplaced candidates, it computes and stores `series_seasons.metadata.fp_plan = {samples:[video_id…], candidates:[video_id…]}`. It uses exactly the rules of `signatures.plan` (`signatures.py:54-92`):
   - `PER_SEASON=4` samples, spread over episode order;
   - candidates within `LENGTH_BAND=0.35`, `duration ≥ 180 s`, air date within ±`DATE_SLACK_DAYS=180` of the season;
   - **after `last_air` there is no limit when `kind='ongoing'`**, matching `series_detector._fingerprint_worth_it` (`:505-507`).

   It returns requests:
   - `fingerprint(v, payload={role:"sample", seasons:[S…]})` for each sample *v*;
   - `fp_gate(S)`.
2. **`fingerprint(v)`** runs only if its input (size, mtime, duration, `SIG_VERSION`) changed. Otherwise it is `skipped_unchanged`. On output change it enqueues:
   - `fp_gate(S)` for each season in `payload.seasons` (debounced 10 s);
   - `detect(L)` (debounced 30 s).
3. **`fp_gate(S)`**:
   - If a sample still has a live fingerprint job, it finishes `waiting_inputs` with output unchanged. The last sample to finish triggers it again.
   - Otherwise it applies `has_signature` (`signatures.py:109-119`), stores `signature ∈ {shared, none}`, and on `shared` requests `fingerprint(c, payload={role:"candidate", seasons:[S]})` for each candidate.
4. **Candidate fingerprints** that change re-trigger `detect(L)`. The detector now uses them as evidence (`_signature_match`), and the cycle closes.

**Termination.** With no external change (no file, listing or version change), each video's `fingerprint` input fingerprint is fixed. So each video's fingerprint output changes **at most once** (the first successful run). After that every rerun is `skipped_unchanged`, which never propagates.

`detect` is enqueued only by output changes of its inputs. With no external change, the number of such changes is bounded by `|videos| × |video stages|`. Failed and partial fingerprints are failures (`fp_partial` maps to failed with cause `nas_partial_read`, as in `signatures.py:142-149`), and failures never propagate. They retry up to `max_attempts` and then stop.

`fp_gate`'s requests target only videos whose fingerprint never succeeded. No edge or request leads from a stage back to itself. Hence every cascade is finite: there are at most |V| fingerprint changes, each followed by one coalesced `detect`. In practice the flow converges in two `detect` rounds, as the prototype does (`pipeline.py:121-130`).

The same argument covers `rate_style ⇄ style_sample`. `rate_style` requests at most 3 samples per season, and up to 8 when borderline (`scan_visual.py`). Each sample's output changes at most once.

### 3.5 Legacy integration

The existing handlers keep calling `db.jobs.enqueue(video_id=…, stage=…)` and `enqueue_scan`. Those two become thin wrappers over `engine.enqueue.enqueue_job(subject_kind, subject_id, stage, …)`, keeping their signatures and the `EnqueueResult.outcome` values `inserted | reused | skipped_done_unchanged`. Mapping:

| New outcome | Old outcome |
|---|---|
| `coalesced` | `reused` |
| `rerun_requested` | `reused` |

The existing `skipped_done_unchanged` pre-check (`db/jobs.py:218-229`, comparing `videos.updated_at` with `finished_at`) is kept for legacy stages only.

After a legacy handler marks its row `done`, the dispatch wrapper (`runtime.build_default_dispatch`) calls `engine.propagate.after_legacy_done(db, job)`. That function:

1. computes `output_fingerprint`;
2. writes `output_hash`, `outcome` and `propagated_at`;
3. enqueues the non-legacy edges;

all in one transaction. A crash between the handler's `mark_done` and this step is repaired by the reconciler (§6.5).

---

## 4. Data model (contract shared by A, B and C)

Postgres types are shown. The SQLite twins apply the swaps in `0002_processing_jobs.sqlite.sql:6-12`, with `TEXT[]` → `TEXT` (a JSON array).

### 4.1 `processing_jobs`: new columns (slot 0088)

| Column | Type | Default | Meaning |
|---|---|---|---|
| `subject_kind` | `TEXT NOT NULL` | `'video'` | `video, library, series, season, catalog` |
| `subject_id` | `UUID` | NULL (backfilled) | equals the typed column for the kind; `CATALOG_ID` for catalog |
| `series_id` | `UUID REFERENCES series(id) ON DELETE CASCADE` | NULL | set iff `subject_kind='series'` |
| `season_id` | `UUID REFERENCES series_seasons(id) ON DELETE CASCADE` | NULL | set iff `subject_kind='season'` |
| `stage_version` | `INT` | NULL | `StageDef.version` the job ran with (set at finish) |
| `input_hash` | `TEXT` | NULL | value of `input_fingerprint` at run time |
| `output_hash` | `TEXT` | NULL | `StageResult.output_hash` |
| `outcome` | `TEXT` | NULL | `changed, unchanged, skipped_unchanged, awaiting_review, waiting_inputs, imported, superseded` (CHECK `processing_jobs_outcome_chk`) |
| `output_summary` | `JSONB` | NULL | small, stage-defined result for the UI (e.g. `{"placed":12,"seasons_changed":2}`, or the review diff) |
| `force` | `BOOLEAN NOT NULL` | `false` | ignore the skip check |
| `force_cascade` | `BOOLEAN NOT NULL` | `false` | propagate even when the output is unchanged, and pass `force_cascade` to dependents ("from the start") |
| `reasons` | `JSONB NOT NULL` | `'[]'` | list of `{at, why, by, from_job?, from_stage?}`, newest first, capped at 20 entries |
| `enqueue_count` | `INT NOT NULL` | `1` | number of triggers coalesced into this row |
| `triggered_by` | `BIGINT` | NULL | first job id that caused this row (no FK; history survives pruning) |
| `triggered_by_stage` | `TEXT` | NULL | that job's stage, used for arrow flow/min |
| `rerun_requested` | `BOOLEAN NOT NULL` | `false` | D6 |
| `rerun_force` | `BOOLEAN NOT NULL` | `false` | |
| `rerun_reasons` | `JSONB NOT NULL` | `'[]'` | |
| `propagated_at` | `TIMESTAMPTZ` | NULL | set in the same transaction as the dependents' enqueue (outbox marker) |
| `error_cause` | `TEXT` | NULL | normalized cause code (§6.3) |
| `progress` | `JSONB` | NULL | `{step, steps:[…], done_units, total_units, message}`, updated at most every 2 s |
| `started_at` | `TIMESTAMPTZ` | NULL | `claimed → running` time, for durations |
| `updated_at` | `TIMESTAMPTZ` | `now()` | **added in 0087** (fixes F7); kept current by trigger `processing_jobs_touch_trg` BEFORE UPDATE |

**Constraints:**

- `processing_jobs_subject_chk`, added NOT VALID then VALIDATE:

  ```sql
  subject_id IS NOT NULL AND (
     (subject_kind='video'   AND video_id   = subject_id)
  OR (subject_kind='library' AND library_id = subject_id AND video_id IS NULL)
  OR (subject_kind='series'  AND series_id  = subject_id AND video_id IS NULL)
  OR (subject_kind='season'  AND season_id  = subject_id AND video_id IS NULL)
  OR (subject_kind='catalog' AND subject_id = '00000000-0000-0000-0000-00000000ca7a'::uuid AND video_id IS NULL))
  ```

  `library_id` may additionally be set on video, season and series rows as a denormalized filter column (written by the enqueue function). This is allowed by the CHECK above, which constrains `library_id` only for the library kind.
- `processing_jobs_scope_chk` (0058) is **dropped**. It forbade library-level stages other than `scan`; the subject CHECK supersedes it.
- `processing_jobs_stage_fk FOREIGN KEY (stage) REFERENCES job_stages(stage)`, added in 0087, NOT VALID then VALIDATE.

**Indexes** (all `CREATE … CONCURRENTLY IF NOT EXISTS`; the file runs with goose's no-transaction directive):

| Name | Definition | Used by |
|---|---|---|
| `processing_jobs_one_live_per_subject_stage` | UNIQUE `(subject_kind, subject_id, stage) WHERE state IN ('pending','claimed','running','resuming','paused')` | enqueue arbiter |
| `processing_jobs_subject_history_idx` | `(subject_kind, subject_id, stage, id DESC)` | last-done lookup, item history |
| `processing_jobs_stage_state_idx` | `(stage, state, priority, id)` | claim by pool, queue detail |
| `processing_jobs_finished_idx` | `(stage, finished_at) WHERE finished_at IS NOT NULL` | rate per 10 min |
| `processing_jobs_failed_cause_idx` | `(stage, error_cause) WHERE state = 'failed'` | issues |
| `processing_jobs_unpropagated_idx` | `(id) WHERE state = 'done' AND propagated_at IS NULL` | reconciler |
| `processing_jobs_library_idx` | `(library_id, stage, state)` | libraries table |
| `processing_jobs_created_idx` | `(created_at)` | flow/min, prune |

`processing_jobs_one_live_per_video_stage` (0002) and `processing_jobs_one_live_scan_per_library` (0058) are **kept** in 0088. Every row they constrain is also constrained by the subject index, so they never disagree. Slot 0091 drops them after A and B have switched every INSERT to the function. That keeps the A and B rollouts independent.

**Notify trigger.** `processing_jobs_notify_new()` is replaced (`CREATE OR REPLACE`) so the `jobs.new` payload carries `{id, video_id, library_id, subject_kind, subject_id, stage, priority}`. It is a superset, so current listeners are unaffected.

### 4.2 `processing_jobs_enqueue`: SQL function (Postgres, slot 0088)

```sql
processing_jobs_enqueue(
    p_subject_kind   TEXT,
    p_subject_id     UUID,
    p_stage          TEXT,
    p_priority       INT     DEFAULT NULL,   -- NULL → job_stages default 100
    p_force          BOOLEAN DEFAULT false,
    p_force_cascade  BOOLEAN DEFAULT false,
    p_reason         JSONB   DEFAULT '{}',   -- {"why": "...", "by": "user:<id>|job:<id>|system", "from_stage": "..."}
    p_triggered_by   BIGINT  DEFAULT NULL,
    p_payload        JSONB   DEFAULT NULL,
    p_max_attempts   INT     DEFAULT NULL
) RETURNS TABLE (job_id BIGINT, outcome TEXT)
  -- outcome ∈ inserted | coalesced | rerun_requested | disabled | unknown_stage | invalid_subject
```

Semantics, normative for the SQLite Python twin (`engine/enqueue.py::_enqueue_sqlite`):

1. Look up `job_stages` by `p_stage`.
   - Missing → return `unknown_stage`.
   - `enabled=false` → return `disabled`.
   - `p_subject_kind` not in `subject_kinds` → `invalid_subject`.
2. Resolve the typed columns:
   - video → `video_id = p_subject_id`, `library_id = videos.library_id`;
   - season → `season_id`, `library_id = series_seasons.library_id`;
   - series → `series_id`, `library_id = series.library_id`;
   - library → `library_id`;
   - catalog → nothing.

   A missing subject row → `invalid_subject`.
3. Run:

   ```sql
   INSERT … (state='pending', not_before = CASE WHEN debounce_sec>0 THEN now()+debounce_sec END,
             reasons = jsonb_build_array(p_reason || {at: now()}), triggered_by, triggered_by_stage …)
   ON CONFLICT (subject_kind, subject_id, stage)
      WHERE state IN ('pending','claimed','running','resuming','paused')
   DO UPDATE SET
       priority      = CASE WHEN pj.state='pending' THEN LEAST(pj.priority, EXCLUDED.priority) ELSE pj.priority END,
       force         = pj.force OR (pj.state='pending' AND EXCLUDED.force),
       force_cascade = pj.force_cascade OR (pj.state='pending' AND EXCLUDED.force_cascade),
       reasons       = CASE WHEN pj.state='pending' THEN <prepend, cap 20> ELSE pj.reasons END,
       enqueue_count = pj.enqueue_count + 1,
       not_before    = CASE WHEN pj.state='pending' AND s.debounce_sec>0
                            THEN LEAST(now()+s.debounce_sec*interval '1s', pj.created_at+s.debounce_max_sec*interval '1s')
                            ELSE pj.not_before END,
       rerun_requested = pj.rerun_requested OR pj.state <> 'pending',
       rerun_force     = pj.rerun_force OR (pj.state <> 'pending' AND (EXCLUDED.force OR EXCLUDED.force_cascade)),
       rerun_reasons   = CASE WHEN pj.state <> 'pending' THEN <prepend, cap 20> ELSE pj.rerun_reasons END
   RETURNING id, CASE WHEN xmax = 0 THEN 'inserted'
                      WHEN state = 'pending' THEN 'coalesced'
                      ELSE 'rerun_requested' END
   ```

   Here `pj` is the existing row and `s` the `job_stages` row. A coalesce keeps the original payload unless the existing payload is NULL. Requesters that need union semantics (`payload.seasons`) send arrays, and the function merges `payload.seasons` as a set union. That merge is the only payload key with defined merge behaviour.
4. On `inserted`, the existing `jobs.new` trigger fires. On SQLite, Python publishes on the in-process bus, as `db/jobs.py:322-333` does today.

**Retry.** Operator retry, both Go and Python, becomes `processing_jobs_enqueue(kind, id, stage, p_force => true, p_reason => {"why":"retry","of_job":<id>})`. The failed row is left untouched as history, but it is no longer the latest row for its subject, so it drops out of the open issues (§6.3). This removes the update-in-place in `jobs.go:268-272` and `jobs_state.retry_failed`. That function is kept but no longer called by the API.

### 4.3 Control tables (slots 0087 and 0089)

```sql
-- 0087
CREATE TABLE IF NOT EXISTS worker_pools (
    pool          TEXT PRIMARY KEY CHECK (pool ~ '^[a-z][a-z0-9_]{1,31}$'),
    label         TEXT NOT NULL,
    scope         TEXT NOT NULL CHECK (scope IN ('global','per_worker')),
    size          INT  CHECK (size IS NULL OR size BETWEEN 0 AND 64),   -- NULL = auto (cpu: cores-1, min 1)
    default_size  INT,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by    TEXT
);
-- seed: ('nas_read','NAS reads','global',2,2), ('cpu','CPU','per_worker',NULL,NULL),
--       ('gpu','GPU','per_worker',1,1), ('network_plex','Plex','global',2,2),
--       ('network_tv','dizqueTV','global',1,1), ('network_yt','YouTube','global',1,1)

CREATE TABLE IF NOT EXISTS job_stages (
    stage            TEXT PRIMARY KEY CHECK (stage ~ '^[a-z][a-z0-9_]{1,39}$'),
    label            TEXT NOT NULL,
    subject_kinds    TEXT[] NOT NULL,
    pool             TEXT NOT NULL REFERENCES worker_pools(pool),
    lane             TEXT NOT NULL CHECK (lane IN ('sources','episodes','seasons','channels','media')),
    lane_order       INT  NOT NULL DEFAULT 0,
    version          INT  NOT NULL DEFAULT 1,
    debounce_sec     INT  NOT NULL DEFAULT 0,
    debounce_max_sec INT  NOT NULL DEFAULT 0,
    max_attempts     INT  NOT NULL DEFAULT 3,
    edges            JSONB NOT NULL DEFAULT '[]',   -- [{to, map, when, legacy}]
    may_request      TEXT[] NOT NULL DEFAULT '{}',
    entry_for        TEXT[] NOT NULL DEFAULT '{}',
    legacy           BOOLEAN NOT NULL DEFAULT false,
    hidden           BOOLEAN NOT NULL DEFAULT false,
    external_write   BOOLEAN NOT NULL DEFAULT false,
    -- operator-controlled (graph sync NEVER writes these)
    enabled          BOOLEAN NOT NULL DEFAULT true,
    disabled_reason  TEXT,                         -- not_configured | operator
    paused           BOOLEAN NOT NULL DEFAULT false,
    paused_at        TIMESTAMPTZ,
    paused_by        TEXT,
    write_mode       TEXT NOT NULL DEFAULT 'apply' CHECK (write_mode IN ('apply','review')),
    graph_synced_at  TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- seed: every stage in §3.2. The three external_write stages are seeded with write_mode='review'.
```

**Graph sync rule** (Python, `engine/sync.py`): `INSERT … ON CONFLICT (stage) DO UPDATE SET label, subject_kinds, pool, lane, lane_order, version, debounce_*, max_attempts, edges, may_request, entry_for, legacy, hidden, external_write, graph_synced_at`. It never touches the operator columns, except `enabled` and `disabled_reason` **when `disabled_reason` is NULL or `'not_configured'`**. The worker may flip `not_configured`, but never an operator disable. Stages missing from the graph keep their rows; the UI shows them as stale if `graph_synced_at` is older than 1 day.

```sql
-- 0089
CREATE TABLE IF NOT EXISTS queue_control (
    id         SMALLINT PRIMARY KEY CHECK (id = 1),
    paused     BOOLEAN NOT NULL DEFAULT false,
    paused_at  TIMESTAMPTZ,
    paused_by  TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);  -- seed (1,false)

-- AFTER INSERT OR UPDATE on worker_pools, job_stages and queue_control:
--   pg_notify('jobs.control', json_build_object('table', TG_TABLE_NAME, 'key', <pk>)::text)

CREATE TABLE IF NOT EXISTS job_events (
    id            BIGSERIAL PRIMARY KEY,
    job_id        BIGINT NOT NULL,                 -- no FK: logs outlive pruned job rows
    subject_kind  TEXT   NOT NULL,
    subject_id    UUID   NOT NULL,
    library_id    UUID,
    stage         TEXT   NOT NULL,
    at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    level         TEXT   NOT NULL CHECK (level IN ('debug','info','warn','error')),
    kind          TEXT   NOT NULL CHECK (kind IN ('enqueued','coalesced','rerun_requested','claimed',
                       'step','skipped','done','failed','retry_scheduled','cancelled','paused',
                       'resumed','reaped','triggered','requested','review','applied','imported')),
    message       TEXT   NOT NULL,                 -- plain English, ≤ 500 chars
    data          JSONB
);
-- indexes: job_events_job_idx (job_id, id); job_events_stage_idx (stage, id DESC);
--          job_events_subject_idx (subject_kind, subject_id, id DESC);
--          job_events_library_idx (library_id, id DESC) WHERE library_id IS NOT NULL;
--          job_events_at_idx (at)

CREATE TABLE IF NOT EXISTS health_samples (
    at      TIMESTAMPTZ NOT NULL,
    host    TEXT NOT NULL,
    metric  TEXT NOT NULL,
    value   DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (metric, host, at)
);
-- index: health_samples_at_idx (at)
```

**`job_events.kind='triggered'` data:** `{"to":[{"stage":"detect","subject_kind":"library","subject_id":"…","outcome":"coalesced"}…]}`. The history page uses it to show "what each re-queued".

### 4.4 Analysis store (slot 0090)

Workstream A writes these tables; B reads them for history and labels.

| Table | Columns (key first) | Written by |
|---|---|---|
| `series_seasons` | `id UUID PK` (uuid5(`NS_SEASON`, `f"{library_id}\|{series_key}\|{kind}\|{season}"`)), `series_id UUID NOT NULL → series ON DELETE CASCADE`, `library_id UUID → libraries ON DELETE CASCADE`, `season INT NOT NULL`, `kind TEXT NOT NULL CHECK IN ('episodic','ongoing','short-form','clips')`, `name TEXT NOT NULL`, `first_air DATE`, `last_air DATE`, `median_duration_s REAL`, `episode_count INT NOT NULL DEFAULT 0`, `membership_hash TEXT NOT NULL`, `detector_version INT NOT NULL`, `metadata JSONB NOT NULL DEFAULT '{}'` (`playlists, extras, attached, duplicates, fp_plan, signature`), `retired_at TIMESTAMPTZ` (season vanished; kept so cleanup jobs can run), `created_at`, `updated_at`. `UNIQUE (series_id, season)`. Index `(library_id)`. | `detect`, `fp_gate` |
| `series` (existing) | `detect` upserts with `id = uuid5(NS_SERIES, f"{library_id}\|{series_key}\|{kind}")` and `metadata.series_key`, `metadata.kind` | `detect` |
| `series_episodes` (existing) | atomic replace per library in `detect` | `detect` |
| `video_signals` | `video_id UUID PK → videos ON DELETE CASCADE`, `thumb_dhash TEXT` (16 hex), `thumb_version INT`, `audio_head TEXT`, `audio_tail TEXT` (base64 chromaprint), `frame_hashes TEXT` (comma hex), `fp_version INT`, `fp_partial BOOLEAN NOT NULL DEFAULT false`, `style_metrics JSONB`, `style_version INT`, `updated_at` | `thumb_hash`, `fingerprint`, `style_sample` |
| `youtube_channels` | `channel_id TEXT PK`, `library_id UUID → libraries ON DELETE CASCADE`, `url TEXT NOT NULL`, `title TEXT`, `status TEXT CHECK IN ('ok','gone','error','bot_check')`, `error TEXT`, `fetched_at` | `youtube_sync` |
| `youtube_videos` | `youtube_id TEXT PK`, `channel_id TEXT`, `title TEXT NOT NULL`, `upload_date DATE`, `date_quality TEXT CHECK IN ('exact','approximate')`, `duration_s REAL`, `tab TEXT`, `description TEXT`, `source TEXT CHECK IN ('info_json','youtube')`, `fetched_at` | `youtube_sync`, `link` (info_json) |
| `youtube_playlists` | `playlist_id TEXT PK`, `channel_id TEXT`, `title TEXT NOT NULL`, `item_count INT`, `is_real BOOLEAN NOT NULL`, `source TEXT`, `fetched_at` | `youtube_sync` |
| `youtube_playlist_items` | `(playlist_id, youtube_id) PK`, `position INT`; index `(youtube_id)` | `youtube_sync` |
| `media_metadata_enrichment` (existing, slot 0077) | link writes `provider='youtube'`, `external_id=<youtube_id>`, `mapped={link_method, title, upload_date, date_quality}`, `confidence` (1.0 info_json/exact, 0.9 folded, 0.8 title+duration, 0.6 fuzzy), `is_accepted=true` only when no `media_field_provenance` user row exists (slot 0078 "never overwrite a user edit") | `link` |
| `season_tags` | `(season_id, tag) PK`, `score REAL NOT NULL`, `evidence JSONB` | `tag` |
| `video_tags` | `(video_id, tag) PK`, `score REAL NOT NULL` | `tag` (library) |
| `season_style` | `season_id UUID PK → series_seasons ON DELETE CASCADE`, `style TEXT CHECK IN ('static','talk','produced','cinematic')`, `tier SMALLINT`, `confidence REAL`, `reasons JSONB`, `sampled INT`, `style_version INT`, `override_style TEXT`, `updated_at` | `rate_style`; override via API |
| `external_links` | `(target, subject_kind, subject_id) PK`, `target TEXT CHECK IN ('plex_collection','dizquetv_show','maktaba_channel','dizquetv_channel')`, `external_id TEXT NOT NULL`, `title TEXT NOT NULL`, `content_hash TEXT NOT NULL`, `created_by_tool BOOLEAN NOT NULL`, `updated_at` | `plex_collection`, `dizquetv_show`, `write_channels` |
| `channel_plans` | `id BIGSERIAL PK`, `created_at`, `planner_version INT`, `input_hash TEXT NOT NULL`, `plan JSONB NOT NULL` (the `build_plan_json` shape of `scripts/mediaintel/plan_channels.py:317`); keep the latest 20 | `plan_channels` |

The pure analysis code is called unchanged. The new stage modules port only the I/O half of each mediaintel script and swap its SQLite cache for these tables:

| Stage | Pure code | Ported I/O from |
|---|---|---|
| `thumb_hash` | `thumbnail/dhash.py` | `scan_local._thumb_dhash` |
| `fingerprint` | `audio/chromaprint.py`, `dhash.frame_times` | `scan_local._signature` |
| `style_sample` | `classify/production_style.py` | `scan_visual.sample_file` |
| `link` | `text/arabic.fold`, `ytdlp_filename` | `link.link` |
| `detect` | `classify/series_detector.detect` | `detect.load_facts/store` |
| `tag` | `classify/topic_tags` | `classify_tags._tag_folder` |
| `rate_style` | `production_style.classify_style` | `scan_visual.run` |
| `plan_channels` | `schedule/channel_planner.suggest_channels` | `plan_channels.load_inputs` |
| `plex_collection` | – | `export_plex.export` |
| `dizquetv_show` | – | `dizquetv_shows.run` |
| `write_channels` | – | `export_dizquetv.plan_actions/apply_actions` + `channel_planner.maktaba_channel_definition` |

---

## 5. Worker pools and claiming

### 5.1 Pool assignment

Pools are shown in §3.2. Legacy `extract` takes a `nas_read` slot because it reads the whole file over the share. It competes with probes by priority (lower number wins), which is the intended NAS protection.

### 5.2 Claim contract (`engine/claim.py`, replaces the call site in `pipeline/runner.py:_drain`)

```python
async def claim_for_pool(db, *, worker_id: str, pool: str, stages: Sequence[str]) -> Job | None
```

Postgres, run in one transaction:

```sql
SELECT pg_advisory_xact_lock(hashtext('maktaba.pool:' || $2));      -- global pools only
UPDATE processing_jobs pj
   SET state='claimed', claimed_by=$1, claimed_at=now(), last_heartbeat_at=now(), attempts=attempts+1
 WHERE pj.id = (
   SELECT j.id FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage
    WHERE s.pool = $2 AND j.stage = ANY($3::text[])
      AND s.enabled AND NOT s.paused
      AND NOT (SELECT paused FROM queue_control WHERE id = 1)
      AND j.state IN ('pending','paused') AND j.pause_requested = false AND j.cancel_requested = false
      AND (j.not_before IS NULL OR j.not_before <= now())
      AND (   (SELECT scope FROM worker_pools WHERE pool=$2) = 'per_worker'
           OR (SELECT count(*) FROM processing_jobs b JOIN job_stages bs ON bs.stage=b.stage
                WHERE bs.pool=$2 AND b.state IN ('claimed','running','resuming'))
              < (SELECT COALESCE(size, default_size, 1) FROM worker_pools WHERE pool=$2))
    ORDER BY j.priority, j.id
    FOR UPDATE OF j SKIP LOCKED LIMIT 1)
RETURNING pj.*;
```

The eligibility predicate is otherwise identical to `jobs_claim.py:63-82`.

SQLite: the same predicate inside the existing `asyncio.Lock` + `BEGIN IMMEDIATE` (`jobs_claim.py:151-170`). No advisory lock is needed.

`Job.stage` becomes `str`. `Stage` stays an alias enum for legacy call sites. `_row_to_job` must not coerce unknown stages through `Stage(...)` (`jobs_claim.py:211`), because a new stage would raise there.

### 5.3 Runner changes

`ClaimLoop` keeps one `asyncio.Semaphore` per `per_worker` pool, sized from `worker_pools.size` (cpu `NULL` → `os.cpu_count()-1`, minimum 1).

At each drain it:

1. reloads pool sizes;
2. resizes a semaphore by replacing it when its size changed (in-flight jobs release into the old one);
3. iterates pools in fixed order `gpu, nas_read, network_*, cpu`, so a slow pool cannot starve claims.

`PgListenWakeup` also LISTENs `jobs.control`.

`--stages` remains as an optional filter. The default is every stage in the graph with a handler.

---

## 6. Engine behaviour

### 6.1 Job lifecycle, as implemented by `engine/run.py::execute(db, job)`

1. Mark `running` and set `started_at`. Insert `job_events(kind='claimed')` and `events(job.started)`.
2. If `input_fingerprint` is set and `not job.force and not job.force_cascade`:
   1. compute `ih`;
   2. load the last `done` row for the subject and stage;
   3. if its `stage_version == version` and `input_hash == ih`, finish as `outcome='skipped_unchanged'` with `output_hash = prev.output_hash` and `propagated_at=now()`. Insert `job_events(kind='skipped', level='debug')`. Do not publish an SSE event (§7.2).
3. Call `stage.run(db, job, ctx)`.
   - `ctx` gives `progress(step, done, total, message)` (throttled 2 s), `log(level, message, data)`, `nas` (§6.6) and `check_cancel()`.
   - The heartbeat wraps the call, as `runtime.py:209-211` does today.
4. On success, in one transaction (`engine/propagate.py::finish_success`):
   1. `changed = force_cascade or prev is None or prev.output_hash != result.output_hash`.
   2. UPDATE the row:
      - `state='done'`, `finished_at=now()`, `stage_version`, `input_hash`, `output_hash`, `output_summary`;
      - `outcome = result.outcome or ('changed' if changed else 'unchanged')`;
      - `propagated_at=now()`.
   3. If `changed`: enqueue every edge target with `p_reason={"why":"<stage> output changed","by":"job:<id>","from_stage":"<stage>"}`, `p_triggered_by=id` and `p_force_cascade=job.force_cascade`.
   4. Enqueue every `result.requests` entry. A target outside `may_request` raises `GraphViolation`.
   5. If `rerun_requested`: enqueue the same subject and stage with `p_force=rerun_force` and reasons from `rerun_reasons`.
   6. Insert `job_events`: `done`, and `triggered` (data.to = all enqueue results) if anything was enqueued.
   7. Insert `events(job.finished)`.
5. On failure: `mark_failed_or_retry` (unchanged backoff) also sets `error_cause` (§6.3) and inserts `job_events` (`failed` or `retry_scheduled`) and `events(job.failed)`.
   - **Terminal failure** (attempts exhausted, or non-retryable): if `rerun_requested`, enqueue the follow-up. New input deserves a fresh attempt.
   - **Retryable failure** with `rerun_requested`: the row goes back to `pending` as today. The flag is cleared, because the retry is the follow-up.
6. Cancel: `cancelled`, plus a follow-up if `rerun_requested`.

### 6.2 Retrigger semantics (used by the API)

| Request | Effect |
|---|---|
| `{subject_kind, subject_id, stage}` | enqueue that stage; the skip check decides if work runs ("re-adds it to the right queue") |
| `… , mode:"force"` | `p_force=true`: rerun the stage, propagate only on change |
| `… , mode:"force_cascade"` | `p_force_cascade=true`: rerun it and everything downstream ("everything from the start") |
| `stage` omitted | enqueue every stage whose `entry_for` contains the subject kind; series expands to its seasons |
| bulk | `items[]` of up to 5000 subjects, run in one transaction |

### 6.3 Error-cause normalization (`engine/causes.py`)

`error_cause` is computed once, at failure time:

1. **Known patterns** (first match wins; regexes over `kind + " " + message`, case-insensitive):

   | Cause | Matches |
   |---|---|
   | `nas_timeout` | `timed out`, `ETIMEDOUT`, `WinError 121`, `semaphore timeout`, `STATUS_IO_TIMEOUT` |
   | `nas_invalid_argument` | `Invalid argument` with an ffmpeg input error (the SMB failure in `signatures.py:143`) |
   | `nas_partial_read` | stage-raised (`fp_partial`) |
   | `file_missing` | `No such file`, `FileNotFoundError`, `WinError 2/3` |
   | `ffmpeg_decode` | `Invalid data found`, `moov atom not found`, `Error while decoding` |
   | `youtube_bot_check` | `confirm you're not a bot`, `Sign in to confirm` |
   | `youtube_unavailable` | `Video unavailable`, `This channel does not exist`, `HTTP Error 404` |
   | `plex_unreachable` | connection refused or timeout to `MAKTABA_PLEX_URL` |
   | `plex_auth` | HTTP 401/403 from Plex |
   | `dizquetv_unreachable` | connection refused or timeout to `MAKTABA_DIZQUETV_URL` |
   | `readback_mismatch` | the verify step of a Plex or dizqueTV write |
   | `no_handler` | `dispatch_unknown_stage` |
   | `graph_violation` | an undeclared request |
   | `db_error` | `asyncpg`/`sqlite3` exceptions |
2. **Otherwise** `cause = f"{kind}:{norm(message)}"`, where `norm` does the following in order:
   1. lower-case;
   2. replace quoted strings, Windows paths (`[A-Za-z]:[\\/]…`), UNC paths and POSIX absolute paths with `<path>`;
   3. replace UUIDs with `<id>`, hex runs of 8 or more with `<hex>`, numbers with `<n>`;
   4. collapse whitespace;
   5. truncate to 120 characters.
3. **Rows from before this migration** (cause NULL): the API groups them by `error::jsonb->>'kind'`. Go handles both a JSON and a plain-text `error`.

**Issue groups** are computed by the API. Each group is `(stage, error_cause)` over subjects whose **latest** row for the stage is `failed`, using `DISTINCT ON (subject_kind, subject_id, stage) … ORDER BY id DESC`. Two pseudo-causes are added:

- `awaiting_review`: latest row `done` with `outcome='awaiting_review'`; the action is Apply.
- `stale_graph`: a stage with no worker handler; the action is "Show workers".

### 6.4 Retention (`engine/prune.py`, run by the reaper loop every hour)

| What | Kept for |
|---|---|
| `job_events` at debug | 7 days |
| `job_events` at info | 30 days |
| `job_events` at warn/error | 90 days |
| `processing_jobs` terminal rows | 90 days, **except** the latest `done` row per `(subject_kind, subject_id, stage)`, which skip checks need and which is kept forever |
| `health_samples` | 48 hours (10 s samples); a 10-min rollup row per metric and host is kept for 30 days (`host='*rollup*'`) |
| `channel_plans` | latest 20 |

Deletes run in batches of 5000 per transaction.

### 6.5 Reconciler (`engine/reconcile.py`, every 60 s, leader-elected via `pg_try_advisory_lock(hashtext('maktaba.reconciler'))`)

1. `done` rows with `propagated_at IS NULL` older than 60 s: compute the output fingerprint if it is missing, propagate, and set `propagated_at`. This covers a crash between a legacy `mark_done` and propagation.
2. Enqueue `youtube_sync` for libraries whose last success is older than `MAKTABA_YT_REFRESH_HOURS` (default 168), with reason `scheduled refresh`.
3. Version sweep: after a graph sync bumps a stage `version`, enqueue that stage for every subject that has a `done` row at the old version. Batches of 1000, priority 500 (bulk backfill, architecture §7.5).

### 6.6 NAS and health measurement (`engine/health.py`)

**NAS measurement.** `ctx.nas.timed(path)` is an async context manager used by every `nas_read` stage around its ffmpeg or ffprobe subprocess.

- Before spawning, it measures `os.stat` + `open` + a 64 KiB read and reports that time as `nas.open_ms`.
- A spawn whose stderr matches the `nas_*` causes increments `nas.timeouts`.
- `nas.reads_busy` is the current count of `nas_read` jobs in `claimed/running`, taken from the DB.

Each worker writes `health_samples` every 10 s:

| Metric | Value |
|---|---|
| `nas.open_ms_p50`, `nas.open_ms_p95` | over the last 10 s, rolling |
| `nas.timeouts` | count in the window |
| `nas.reads_busy` | |
| `cpu.pct` | `psutil.cpu_percent`, if installed; otherwise `os.getloadavg()[0]/cpu_count*100`; omitted on Windows without psutil |
| `net.plex_ms`, `net.tv_ms`, `net.youtube_ms` | HEAD/GET probe every 60 s per configured integration; `-1` = unreachable |

**Backlog oldest-waiting** is computed by the API, not sampled: `now() - MIN(created_at)` over `pending` rows whose `not_before` has passed, per stage and overall.

---

## 7. Live events (fixes F8, F9)

### 7.1 Transport

- **Channel `jobs`:** Python inserts into `events(channel='jobs', type, payload)` inside the job's transaction. The existing trigger notifies `ws.events`, and the bus fans out to `/ws/jobs`. On SQLite, Python inserts into the same `events` table (twin exists: `0061_events.sqlite.sql`) and also publishes on the in-process bus. The SQLite build has no Go API, so this only feeds in-process consumers.
- **Channel `queues`:** published by Go only. The `queues.snapshot` event goes to `hub.Publish("queues", …)` every 2 s while `hub` has one or more subscribers on `queues`. It is ephemeral: no `events` row and no replay. On connect, the handler immediately sends one snapshot.

### 7.2 Event types (payload contract)

All events use the existing envelope `{type, at, payload}`. Lifecycle events keep a `job` key, because the current page reads `ev.payload.job` (`ProcessingQueue.tsx:84`).

```ts
// Shared by REST and SSE
interface JobRef {
  id: number;                       // BIGSERIAL; new endpoints use a JSON number
  stage: string;
  state: "pending"|"claimed"|"running"|"paused"|"resuming"|"done"|"failed"|"cancelled";
  subject_kind: "video"|"library"|"series"|"season"|"catalog";
  subject_id: string;
  subject_label: string;            // e.g. video title, "قصة وآية · S2", library name, "Catalog"
  library_id: string | null;
  priority: number;
  attempts: number;
  max_attempts: number;
  outcome: string | null;
  error_cause: string | null;
  created_at: string; started_at: string | null; finished_at: string | null;
  progress: { step: string; steps: string[]; done_units: number; total_units: number; message?: string } | null;
}
```

| `type` | `payload` | When | Volume control |
|---|---|---|---|
| `job.started` | `{job: JobRef}` | claim → running | not sent for jobs that end `skipped_unchanged` |
| `job.progress` | `{job: JobRef}` | `ctx.progress` | at most once every 2 s per job |
| `job.finished` | `{job: JobRef, duration_ms: number, triggered: {stage: string, count: number}[]}` | done (changed / unchanged / awaiting_review / waiting_inputs) | not sent for `skipped_unchanged` (counted in snapshots) |
| `job.failed` | `{job: JobRef, message: string, will_retry: boolean, not_before: string \| null}` | failure | – |
| `job.log` | `{job_id: number, stage: string, subject_label: string, level: "info"\|"warn"\|"error", kind: string, message: string, data?: object}` | `job_events` rows at info and above, except kinds `enqueued`, `coalesced`, `skipped` | – |
| `queue.control` | `{stage: string \| null, paused: boolean, by: string}` (`stage=null` means all) | pause or resume | written by Go via `Bus.Publish` |
| `pool.updated` | `{pool: string, size: number \| null}` | pool resize | written by Go via `Bus.Publish` |
| `queues.snapshot` (channel `queues`) | `QueuesSnapshot` (§8.2) | every 2 s | ephemeral |

The legacy `job.progress` shape of architecture §7.10 is subsumed. Transcribe's `jobs_progress.py` writes `progress` + `job.progress` through the same helper.

### 7.3 Python helper (`engine/events.py`)

```python
async def publish(tx, type: str, payload: dict) -> None
    # INSERT INTO events(channel, type, payload) VALUES ('jobs', $1, $2)
```

### 7.4 Go SSE fixes (B)

1. `ws.go` writes `id: <event_id>\n` before `data:` whenever the event carries `payload._event_id`. Live events delivered by the bus also carry it: `Event.toHubEvent` is changed to inject `_event_id`, which today only the replay adapter does. This makes browser `Last-Event-ID` reconnect work (F9).
2. `wireEventBus` returns the `*eventbus.Bus` and stores it on the deps, so handlers can `Publish` (F8).
3. New route `GET /ws/queues` (channel `queues`).

---

## 8. REST API (Go, workstream B)

All routes are under `/api` and require authentication through the existing chain. Mutations require `IsAdmin`, the same as `jobs.go:522-533`, and return 403 problem+json otherwise. Errors use RFC 9457 problem+json, as elsewhere. All timestamps are RFC 3339 UTC.

### 8.1 Routes

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/queues` | – | `QueuesSnapshot` |
| GET | `/api/queues/graph` | – | `{stages: StageInfo[], pools: Pool[]}` |
| GET | `/api/queues/{stage}` | – | `QueueDetail` |
| GET | `/api/queues/{stage}/rate?hours=24` (1–168) | – | `{bucket_sec: 600, points: RatePoint[]}` |
| GET | `/api/queues/{stage}/jobs?state=pending,running&cursor=&limit=50` (limit ≤ 200) | – | `{items: JobRef[], next: string \| null}` |
| GET | `/api/queues/{stage}/failures` | – | `{groups: FailureGroup[]}` |
| POST | `/api/queues/{stage}/pause?interrupt=false` | – | `204` |
| POST | `/api/queues/{stage}/resume` | – | `204` |
| POST | `/api/queues/pause-all` | – | `204` |
| POST | `/api/queues/resume-all` | – | `204` |
| PATCH | `/api/queues/{stage}` | `{enabled?: boolean, write_mode?: "apply"\|"review"}` | `StageInfo` |
| POST | `/api/queues/{stage}/retry` | `{cause?: string, job_ids?: number[]}` (one required) | `{enqueued: number, coalesced: number}` |
| POST | `/api/queues/{stage}/apply` | `{job_ids?: number[], all?: boolean}` | `{enqueued: number}` (enqueue with `payload={"apply":true,"plan_hash":<output_hash>}`, force) |
| GET | `/api/pools` | – | `{pools: Pool[]}` |
| PUT | `/api/pools/{pool}` | `{size: number \| null}` (0–64) | `Pool` |
| POST | `/api/queue/retrigger` | `RetriggerRequest` | `RetriggerResponse` |
| GET | `/api/issues` | – | `{groups: FailureGroup[]}` (all stages, plus `awaiting_review`) |
| GET | `/api/activity?stage=&library_id=&level=info&cursor=&limit=100` | – | `{items: LogLine[], next: string \| null}` |
| GET | `/api/jobs/{id}/events` | – | `{items: LogLine[]}` |
| GET | `/api/items/{kind}/{id}/history?cursor=&limit=50` | – | `ItemHistory` |
| GET | `/api/queues/libraries` | – | `{items: LibraryQueueRow[]}` (cached 5 s) |
| GET | `/api/queues/health?hours=6` | – | `{series: HealthSeries[]}` |
| GET | `/ws/queues` | – | SSE, `queues.snapshot` |

`POST /api/libraries/{id}/scan` (existing) switches its INSERT to `processing_jobs_enqueue('library', id, 'scan', 50, …)`. Its response stays `{job_id}`.

### 8.2 Schemas

```ts
interface Pool { pool: string; label: string; scope: "global"|"per_worker"; size: number|null;
                 effective_size: number; busy: number; stages: string[]; updated_at: string; updated_by: string|null; }

interface StageInfo { stage: string; label: string; lane: "sources"|"episodes"|"seasons"|"channels"|"media";
  lane_order: number; subject_kinds: string[]; pool: string; version: number; legacy: boolean; hidden: boolean;
  external_write: boolean; write_mode: "apply"|"review"; enabled: boolean; disabled_reason: string|null;
  paused: boolean; paused_at: string|null; paused_by: string|null; debounce_sec: number;
  edges: { to: string; map: string; when: string; legacy: boolean }[]; may_request: string[]; entry_for: string[];
  fed_by: string[];            // stages with an edge or request into this one (computed by Go)
  graph_synced_at: string|null; }

type WaitingReason = "paused"|"paused_all"|"pool_full"|"debouncing"|"backoff"|"upstream"|"disabled"
                   |"not_configured"|"awaiting_review"|"idle"|null;

interface QueueSummary {
  stage: string;
  counts: { pending: number; ready: number; running: number; paused: number;
            failed_open: number; done_24h: number; skipped_24h: number; awaiting_review: number };
  wave: { since: string|null; done: number; total: number };   // ring gauge: done/total of the current backlog
  rate_per_min: number;        // done (excluding skipped) in the last 10 min / 10
  eta_sec: number|null;        // ready / rate; null when rate = 0
  oldest_waiting_sec: number|null;
  waiting_reason: WaitingReason;   // set only when running = 0
  running_jobs: JobRef[];      // up to 5
}

interface Flow { from: string; to: string; per_min: number }   // rows created in the last 10 min by triggered_by_stage→stage, /10

interface HealthNow {
  nas: { reads_busy: number; reads_limit: number; open_ms_p50: number|null; open_ms_p95: number|null; timeouts_10m: number };
  cpu: { pct: number|null };
  network: { plex_ms: number|null; tv_ms: number|null; youtube_ms: number|null };   // -1 = unreachable
  backlog: { ready: number; oldest_waiting_sec: number|null };
  spark: Record<"nas_open_ms"|"nas_timeouts"|"cpu_pct"|"backlog_ready", number[]>;  // last 30 samples, 1 min apart
}

interface QueuesSnapshot { at: string; paused_all: boolean; stages: StageInfo[]; queues: QueueSummary[];
                           pools: Pool[]; flows: Flow[]; health: HealthNow; issues_count: number; }

interface RatePoint { t: string; done: number; failed: number; skipped: number; nas_timeouts: number }

interface QueueDetail { info: StageInfo; summary: QueueSummary;
  kpis: { done_24h: number; failed_24h: number; median_duration_ms: number|null; p95_duration_ms: number|null;
          skipped_share_24h: number; changed_share_24h: number };
  running: JobRef[];           // all running jobs of the stage, with progress
  next_up: JobRef[];           // first 20 by (priority, id) among ready pending rows
  failures: FailureGroup[];
  log: LogLine[];              // last 100
}

interface FailureGroup { stage: string; cause: string; label: string;   // human label from a Go map, fallback = cause
  count: number; first_at: string; last_at: string; sample_message: string;
  sample_subjects: { kind: string; id: string; label: string }[];      // up to 5
  action: "retry"|"apply"|"open_settings"|"show_workers"; }

interface LogLine { id: number; at: string; job_id: number; stage: string; subject_kind: string;
  subject_id: string; subject_label: string; level: "debug"|"info"|"warn"|"error"; kind: string;
  message: string; data: Record<string, unknown> | null; }

interface RetriggerRequest { items: { subject_kind: "video"|"library"|"series"|"season"|"catalog";
  subject_id: string; stage?: string }[]; mode?: "queue"|"force"|"force_cascade"; priority?: number; }   // default mode "queue", priority 50
interface RetriggerResponse { results: { subject_kind: string; subject_id: string; stage: string;
  job_id: number|null; outcome: "inserted"|"coalesced"|"rerun_requested"|"disabled"|"unknown_stage"|"invalid_subject" }[]; }

interface ItemHistory {
  subject: { kind: string; id: string; label: string; library_id: string|null; library_label: string|null };
  kinds: string[];             // chips, e.g. ["episodic"] or ["ongoing"]; video: ["episode","extra","short","unassigned"]
  queues: { stage: string; last_job: JobRef|null; can_requeue: boolean }[];   // "re-queue per queue"
  seasons?: { id: string; label: string; season: number; kind: string; episode_count: number }[];  // series, library
  appears_in: { target: "plex_collection"|"dizquetv_show"|"maktaba_channel"|"dizquetv_channel"|"channel_slot";
                title: string; external_id: string; detail?: string }[];
  timeline: { job: JobRef; duration_ms: number|null; triggered: { stage: string; subject_label: string; outcome: string }[];
              reasons: { at: string; why: string; by: string }[] }[];
  next: string|null; }

interface LibraryQueueRow { library_id: string; name: string; videos: number; seasons: number;
  strip: { stage: string; done: number; total: number; running: number; pending: number; failed: number }[];  // only stages whose subject_kinds intersect video/library/season
  last_activity_at: string|null; }

interface HealthSeries { metric: string; points: { t: string; v: number }[] }
```

### 8.3 Query notes (B)

- **`wave.since`:** `MIN(created_at)` over the stage's live rows. If there are none, it is `now()-24h`.
  - `wave.total` = done since `since` + live + failed_open.
  - `wave.done` = done since `since`, skipped included. This way a re-queued library fills its ring quickly when nothing changed.
- **`waiting_reason`**, in precedence order:
  1. `disabled` / `not_configured`
  2. `paused_all`
  3. `paused`
  4. `awaiting_review` (the stage only has `awaiting_review` latest rows)
  5. `pool_full` (ready > 0 and pool busy ≥ effective size)
  6. `debouncing` (pending > 0, all with `not_before > now()` and `attempts = 0`)
  7. `backoff` (the same with `attempts > 0`)
  8. `upstream` (pending = 0 and any `fed_by` stage has live rows)
  9. `idle`
- **Snapshot cost:** the snapshot is one `GROUP BY stage, state` query plus the per-pool busy count, the flows query and the latest `health_samples`. Target under 20 ms at 1M rows. The live-state partial indexes keep it small, and every count query filters on live states or a time window.
- **Subject labels** (batch joins):
  - videos: `COALESCE(title, filename)`;
  - seasons: `series_seasons.name || ' · S' || season` (`kind='ongoing'` shows the year);
  - libraries: `libraries.name`;
  - catalog: the literal "Catalog".

### 8.4 Bug fixes in existing Go code (B)

| Fix | Change |
|---|---|
| F7 | Keep `updated_at=$2` in `jobs.go`. 0087 adds the column, so the fix is schema-side. Add an integration test that runs each control UPDATE against migrated Postgres. |
| F11 | `Stats` derives stage names from `job_stages`. `eta_sec` = Σ over stages of ready/rate. `workers` = distinct `claimed_by` (split `host/pid/uuid` → host). |
| F12 | `scanJob` also selects `error` and `estimated_remaining_sec`. The legacy `Job.ID` stays a string, so the existing client does not break. New endpoints use numbers. |
| Retry | `Retry` (`jobs.go:245-275`) calls `processing_jobs_enqueue` with `p_force` (§4.2) and returns `{id: "<new id>"}`. The 409 for non-failed rows stays. |
| OpenAPI | Add every §8.1 path to `shared/api/openapi.yaml` and `openapi.json`, with server-relative paths (`/queues…`). |

---

## 9. Web UI (workstream C)

### 9.1 Theming rules

- **Tokens.** Use the app tokens only: `--color-bg`, `--color-fg`, `--color-muted`, `--color-subtle` (surface), `--color-accent`, `--color-accent-fg`, `--color-success`, `--color-warn`, `--color-error` and `--color-border`, plus the `--space-*`, `--radius-*` and `--shadow-*` scales (`web/src/styles/tokens.css`). No hard-coded colours.
- **Fix F23 in `tokens.css`:**
  - add dark values for `--color-success`, `--color-warn` and `--color-error` under `[data-theme="dark"]`, meeting WCAG AA 4.5:1 on `--color-bg`;
  - add aliases `--color-semantic-{accent,fg,muted,subtle,success,warn,error,bg}: var(--color-…)` on `:root`, so design-system components (Badge, Card, ProgressBar) follow the theme.
- **Charts and gauges** are inline SVG that use `currentColor` and CSS classes, following `src/components/charts/Charts.tsx:1-5`. No chart library.
- **Classes and RTL.** Class prefix `mkt-q-`, in a new `web/src/styles/queue.css` imported by the pages. Use logical properties (`margin-inline`, `inset-inline`), since the UI supports `ar`/RTL (`src/lib/i18n.tsx:78-83`).
  - Flow arrows mirror under `[dir="rtl"]`.
  - Every user-supplied title passes through `bidiIsolate` (`i18n.tsx:70-74`).
- **Motion and accessibility:**
  - Respect `prefers-reduced-motion` (`tokens.css:73-78`): no spinning gauges or animated arrows.
  - State is never shown by colour alone. Each state chip carries a text label and a glyph.
- **Strings.** All strings go under `queue.*` keys in `src/i18n/en.json` and `ar.json` (no inline prose; `i18n.tsx:3-4`). Numbers and dates use `n()` and `formatDate()`.

### 9.2 Routes and components

| Route | Page file | Replaces |
|---|---|---|
| `/queue` | `pages/queue/QueueOverview.tsx` | `pages/ProcessingQueue.tsx` (deleted; its test is replaced) |
| `/queue/:stage` | `pages/queue/QueueDetail.tsx` | – |
| `/queue/items/:kind/:id` | `pages/queue/ItemHistory.tsx` | – |

Routes are declared in `src/App.tsx` next to the existing `/queue` route (`App.tsx:89`). The nav entry stays as it is (`AppShell.tsx:21`).

**Components** (`web/src/components/queue/`):

| Component | Role |
|---|---|
| `QueueHeader` | state pill, live clock, Pause all / Resume all, "Queue work…" button opening `RetriggerDialog` |
| `RetriggerDialog` | design-system `Modal`; pick library, season or video by search + stage + mode (queue / force / from the start) → `POST /api/queue/retrigger` |
| `HealthStrip` | 4 tiles (NAS, CPU, Network, Backlog), each with a `Sparkline` |
| `Sparkline` | SVG, 30 points |
| `FlowLanes` | 3 lanes from `StageInfo.lane`, sorted by `lane_order`; `hidden` stages are folded into their predecessor's card as sub-step text; plus a Sources chip row and a collapsed Media lane (Q1) |
| `QueueCard` | `RingGauge` (wave.done / wave.total), done/total text, rate + ETA **or** the waiting reason (i18n `queue.waiting.<reason>`), pool chip (`nas_read 2/2`), click → `/queue/:stage` |
| `FlowArrow` | SVG arrow labelled `per_min` from `flows` |
| `IssueList` | `FailureGroup[]` grouped by cause; one action button per group (retry → `POST …/retry {cause}`, apply → `POST …/apply {all:true}`) |
| `ActivityLog` | live `job.log` lines with a stage chip and a filter (stage, level, library); virtualized at 500 lines; "load older" via `/api/activity?cursor` |
| `LibrariesTable` | `LibraryQueueRow[]`; per-stage status strip of small segmented bars; Re-queue → `RetriggerDialog` prefilled |
| `PoolStepper` | − / + on `PUT /api/pools/{pool}`; shows that the pool is shared ("also used by: …") |
| `RateChart` | 10-min bars of done, a failure overlay and NAS-timeout markers |
| `RunningJobs` | progress bar + `progress.steps` sub-step list |
| `NextUp` | |
| `Timeline` | history entries with the result, duration, "re-queued: …" and reasons |
| `KindChips` | |
| `AppearsIn` | |

### 9.3 Data flow

`web/src/lib/queueApi.ts` holds the typed calls for §8 and the TypeScript types copied from §8.2. `web/src/lib/useQueueStream.ts` provides:

- `useQueuesSnapshot()`
  - GET `/api/queues` once, then subscribe to `queues`.
  - Fallback: if no snapshot arrives for 10 s, poll `/api/queues` every 5 s until one arrives. This is the only polling.
- `useJobStream(filter)`
  - subscribe to `jobs`;
  - batch renders at 1 Hz (same pattern as `ProcessingQueue.tsx:83-99`);
  - keep 500 log lines.

### 9.4 `ws.ts` fixes (F24)

- Share one `Subscription` per channel, with ref-counted listeners.
- Add `"queues"` to `Channel`.
- On a manual reopen, pass `?last_event_id=<last seen payload._event_id>`. The server already accepts that parameter (`ws.go:234-247`).
- Fix `VideoDetail.tsx:284` to subscribe to `library/${video.library_id}`. This is a one-line change and is included in C's file list.

---

## 10. Migrations (workstream A)

Every slot ships `NNNN_x.sql` + `NNNN_x.sqlite.sql`. Each Postgres file containing a CONCURRENTLY index uses goose's no-transaction directive (see `0058:1`). Every statement is idempotent (`IF [NOT] EXISTS`, `DO $$ … pg_constraint …`). No prose comment may contain the literal goose token (`migrations/README.md:92-129`). `MANIFEST.md` claims slots 0087–0091.

### 10.1 `0087_job_stages_registry` (fixes F6, F7)

1. `CREATE TABLE worker_pools`, `job_stages` (§4.3) + seed rows.
2. `ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_stage_chk, … processing_jobs_stage_valid, … processing_jobs_stage_check`.
3. Add `processing_jobs_stage_fk` NOT VALID, then VALIDATE.
4. `ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`. `now()` is non-volatile, so Postgres 11+ applies a fast default with no rewrite.
5. `processing_jobs_touch()` + `processing_jobs_touch_trg` BEFORE UPDATE (`NEW.updated_at := now()`).

SQLite twin: steps 1 and 5. Steps 2–4 are done by the 0088 rebuild.

### 10.2 `0088_processing_jobs_subjects`

1. `ADD COLUMN IF NOT EXISTS` for every §4.1 column except `updated_at`. All are nullable or have constant defaults, so this is metadata-only.
2. `series_seasons` must exist for the `season_id` FK, so it is created here, not in 0090.
3. Backfill, in-migration. The queue table is bounded (about 7 rows per video), and this is a one-time catalog of a few hundred thousand rows:

   ```sql
   UPDATE processing_jobs SET subject_kind='library', subject_id=library_id WHERE stage='scan' AND subject_id IS NULL;
   UPDATE processing_jobs SET subject_id=video_id WHERE subject_kind='video' AND subject_id IS NULL;
   ```

   Then `outcome='changed', propagated_at=finished_at` for existing done rows, so the reconciler does not propagate history.
4. Drop `processing_jobs_scope_chk`; add `processing_jobs_subject_chk` and `processing_jobs_outcome_chk` (NOT VALID + VALIDATE).
5. The indexes of §4.1, CONCURRENTLY.
6. `processing_jobs_enqueue(...)` (§4.2); `CREATE OR REPLACE` of `processing_jobs_notify_new()`.

SQLite twin: a 12-step table rebuild (precedent `0049_…sqlite.sql`, `0083`). The new table:

- has nullable `video_id`;
- has no stage CHECK (the stage FK goes to `job_stages`);
- adds all new columns and `CHECK (subject_kind IN (…))`;
- copies with an explicit column list (not `SELECT *`, because column order changed);
- backfills as in step 3;
- recreates every index, the 0058 one included.

This also closes the F5 parity gap.

### 10.3 `0089_queue_control_and_logs`

`queue_control` (+ seed), the `jobs.control` notify triggers, `job_events`, `health_samples`. SQLite twin: the same tables. The triggers are omitted (SQLite has no NOTIFY); Python publishes `jobs.control` on the bus.

### 10.4 `0090_media_intel_store`

Every §4.4 table except `series_seasons` (created in 0088). DDL only.

**Import of mediaintel results** is a tool, not SQL: `python -m maktaba_pipeline.tools.import_mediaintel --cache <cache.db> --library <uuid|name>… [--path-map PLEX=LOCAL] [--apply]`. It is a dry run by default, printing a per-table diff, per the user's rule.

Precondition: Maktaba has scanned the libraries (videos rows exist; cheap per F25).

Steps:

1. **Match** `files.path` → `videos` by the library root plus the relative path, with POSIX normalization as in `scripts/mediaintel/db.py:148`. Require `files.size == videos.size_bytes` and |mtime Δ| < 2 s. Report unmatched files.
2. **Copy:**

   | From (mediaintel cache) | To (Maktaba) |
   |---|---|
   | `files.thumb_dhash`, `audio_head/tail`, `frame_hashes`, `visual_metrics` | `video_signals` |
   | `yt_*` | `youtube_*` |
   | `files.youtube_id` / `link_method` | `media_metadata_enrichment` |
   | `series` / `series_episodes` | `series`, `series_seasons`, `series_episodes`, with uuid5 ids computed from the Maktaba `library_id` |
   | `series_tags`, `video_tags` | `season_tags`, `video_tags` |
   | `series_style`, `style_overrides` | `season_style` |
   | `plex_collections` | `external_links(target='plex_collection')` |
3. **Write one synthetic `processing_jobs` row per `(subject, stage)` that was done in the cache:**
   - `state='done'`, `outcome='imported'`, `attempts=1`;
   - `stage_version` = the cache version (`index_jobs.version`; `SIG_VERSION` for `signature` → `fingerprint`; `visual_style` → `style_sample`; `thumbnail` → `thumb_hash`);
   - `input_hash` = the **same `input_fingerprint` function** the live stage uses;
   - `output_hash` = canonical hash of the imported output;
   - `propagated_at=now()`.

   Cache rows that failed become `state='failed'` with `error_cause` normalized.

   Because input and version match, every live job for these subjects ends `skipped_unchanged`, so nothing is recomputed. A cache row whose version is behind the current stage version is imported but will rerun once (correctly).
4. Then run one `detect` per imported library with reason `import`. Its input fingerprint equals the imported season state, so it finishes `unchanged` unless the detector version moved.

### 10.5 `0091_drop_legacy_live_indexes`

`DROP INDEX CONCURRENTLY IF EXISTS processing_jobs_one_live_per_video_stage`, `processing_jobs_one_live_scan_per_library`. Merge it only after A's `db/jobs.py` and B's `enqueuer.go` call the function. Both workstreams' final PR checklists contain this item. SQLite twin: `DROP INDEX IF EXISTS`.

---

## 11. Workstreams, file ownership and interfaces

The three workstreams share **no files**. Their interfaces are the DB contract (§4, §10), the event payloads (§7.2) and the REST JSON (§8.2). Each one builds and tests against fixtures of those contracts.

### 11.1 Workstream A: DB + Python engine

**Owns, new files:**

- `shared/db/migrations/0087_job_stages_registry.sql`, `0087_job_stages_registry.sqlite.sql`
- `shared/db/migrations/0088_processing_jobs_subjects.sql`, `0088_processing_jobs_subjects.sqlite.sql`
- `shared/db/migrations/0089_queue_control_and_logs.sql`, `0089_queue_control_and_logs.sqlite.sql`
- `shared/db/migrations/0090_media_intel_store.sql`, `0090_media_intel_store.sqlite.sql`
- `shared/db/migrations/0091_drop_legacy_live_indexes.sql`, `0091_drop_legacy_live_indexes.sqlite.sql`
- `pipeline/src/maktaba_pipeline/engine/`: `__init__.py`, `graph.py`, `sync.py`, `enqueue.py`, `claim.py`, `run.py`, `propagate.py`, `events.py`, `causes.py`, `health.py`, `reconcile.py`, `prune.py`, `subjects.py`, `hashing.py`
- `pipeline/src/maktaba_pipeline/engine/stages/`: `thumb_hash.py`, `link.py`, `fingerprint.py`, `style_sample.py`, `youtube_sync.py`, `detect.py`, `fp_gate.py`, `tag.py`, `rate_style.py`, `plex_collection.py`, `dizquetv_show.py`, `plan_channels.py`, `write_channels.py`, `legacy.py` (output fingerprints for legacy stages)
- `pipeline/src/maktaba_pipeline/integrations/`: `plex.py`, `dizquetv.py`, `youtube.py` (ported I/O from `scripts/mediaintel/scan_plex.py`, `export_plex.py`, `dizquetv_shows.py`, `export_dizquetv.py`, `scan_youtube.py`)
- `pipeline/src/maktaba_pipeline/tools/import_mediaintel.py`
- `pipeline/tests/engine/**`, `pipeline/tests/stages/**`, `pipeline/tests/db/test_queue_graph_migrations.py`, `pipeline/tests/tools/test_import_mediaintel.py`

**Owns, modified files:**

- `shared/db/migrations/MANIFEST.md`
- `pipeline/src/maktaba_pipeline/db/jobs.py` (`enqueue` / `enqueue_scan` → wrappers; `Job` gains the new fields; `stage: str`)
- `pipeline/src/maktaba_pipeline/db/jobs_claim.py` (row mapping; delegates to `engine/claim.py`)
- `pipeline/src/maktaba_pipeline/db/jobs_state.py` (`error_cause`, follow-up on terminal failure, `job_events`)
- `pipeline/src/maktaba_pipeline/db/jobs_progress.py` (progress JSON + `job.progress` event)
- `pipeline/src/maktaba_pipeline/pipeline/runner.py` (pools)
- `pipeline/src/maktaba_pipeline/pipeline/wakeup.py` (LISTEN `jobs.control`)
- `pipeline/src/maktaba_pipeline/runtime.py` (dispatch via `engine/run.py`; graph sync; reconciler / prune tasks)
- `pipeline/src/maktaba_pipeline/__main__.py` (default stages = graph)
- `pipeline/src/maktaba_pipeline/handlers.py` (`build_real_dispatch` adds the new stages)
- `pipeline/src/maktaba_pipeline/domain/states.py` (`Stage` enum adds the 0049 and new stage names, for typing only)
- `pipeline/scripts/mediaintel/README.md` (points to the queue)

**Provides to B and C:** the migrated schema, the SQL function, the seeded `job_stages`/`worker_pools` rows, and `events` rows of §7.2.

**Test fixture for B:** `pipeline/tests/fixtures/queue_seed.sql`, a deterministic Postgres seed with jobs in every state, failure causes, events and health samples. B loads it in Go integration tests.

### 11.2 Workstream B: Go API + SSE

**Owns, new files:**

- `api/internal/handlers/queues/`: `queues.go` (Mount + routes), `snapshot.go`, `detail.go`, `rate.go`, `failures.go`, `control.go` (pause, resume, settings, apply, retry), `pools.go`, `retrigger.go`, `activity.go`, `history.go`, `libraries.go`, `health.go`, `causes.go` (cause → label map), `types.go` (§8.2 structs), `stream.go` (2 s snapshot publisher), plus `*_test.go`
- `api/queues_integration_test.go` (`//go:build integration`; loads A's seed)

**Owns, modified files:**

- `api/internal/handlers/jobs/jobs.go` (§8.4)
- `api/internal/handlers/jobs/jobs_test.go`
- `api/internal/handlers/ws/ws.go` (`id:` lines, `/ws/queues`)
- `api/internal/handlers/ws/ws_test.go`
- `api/internal/eventbus/bus.go` (`_event_id` on live events)
- `api/internal/router/p6.go` (mount queues; pass the bus)
- `api/internal/router/p6_eventbus.go` (return the bus)
- `api/internal/handlers/libraries/enqueuer.go` + `enqueuer_test.go` (call the function)
- `shared/api/openapi.yaml`, `shared/api/openapi.json`

**Until A's migrations land**, B develops against a Postgres migrated from this branch's 0087–0090 SQL. If those files are not merged yet, B uses the `queue_seed.sql` DDL header, which A delivers first in Phase 1 (§12).

### 11.3 Workstream C: Web UI

**Owns, new files:**

- `web/src/pages/queue/`: `QueueOverview.tsx`, `QueueDetail.tsx`, `ItemHistory.tsx`, and `*.test.tsx` for each
- `web/src/components/queue/*.tsx` (§9.2) + tests
- `web/src/lib/queueApi.ts`, `web/src/lib/useQueueStream.ts` + tests
- `web/src/styles/queue.css`
- `web/src/test/fixtures/queues.ts` (JSON fixtures for every §8.2 type and §7.2 event)

**Owns, modified files:**

- `web/src/App.tsx`
- `web/src/lib/ws.ts` + `ws.test.ts`
- `web/src/pages/VideoDetail.tsx` (the one-line channel fix)
- `web/src/styles/tokens.css`
- `web/src/i18n/en.json`, `web/src/i18n/ar.json`

**Owns, deleted files:** `web/src/pages/ProcessingQueue.tsx`, `ProcessingQueue.test.tsx`.

**Develops against** the §8.2 fixtures with `vi.mock("../lib/api")` and `vi.mock("../lib/ws")`, the established pattern (`ProcessingQueue.test.tsx:9-27`).

---

## 12. Phased plan

**Phase 1: contract skeletons (≈ 2 days, parallel)**

- **A:** 0087–0089 migrations, `queue_seed.sql`, `graph.py` with every StageDef (`run=None` for the new stages), `sync.py`, `enqueue.py` + the SQL function, the `events.py` publisher wired into the existing `mark_done` / `mark_failed_or_retry`. This alone fixes F6, F7 and F8 for legacy stages.
- **B:** `ws.go`/bus fixes (F9), the `/api/queues` snapshot + `/ws/queues` against the seed, the Stats fix (F11).
- **C:** `tokens.css` fixes (F23), `ws.ts` fixes (F24), `queueApi.ts` types + fixtures, and the `QueueOverview` layout rendering fixture data in both themes.

**Phase 2: engine + read APIs + screens**

- **A:** `claim.py` pools (F10), `run.py` skip / propagate / follow-up, `causes.py`, `job_events`, reconciler, prune, the health sampler, the legacy output fingerprints (`probe → …` edges live).
- **B:** detail, rate, failures, activity, history, libraries and health endpoints; the control endpoints (pause, resume, pools, retrigger, retry, apply, settings).
- **C:** `QueueDetail`, `ItemHistory`, the live log, the dialogs, and pause and pool controls wired to the endpoints.

**Phase 3: new stages + import**

- **A:** stage handlers in dependency order: `thumb_hash`, `youtube_sync`, `link`, `detect`, `fingerprint`, `fp_gate`, `style_sample`, `rate_style`, `tag`, `plan_channels`; then the external writers `plex_collection`, `dizquetv_show`, `write_channels` with review mode. 0090 lands at the start of this phase. Then `import_mediaintel`.
- **B:** `appears_in` and review diff rendering data; the OpenAPI pass.
- **C:** review diff view in Issues → Apply; the Sources and Media lanes; RTL and accessibility pass.

**Phase 4: cleanup.** 0091 (drop the legacy indexes) after A and B confirm that every INSERT uses the function. Retire the `mediaintel run` orchestration (`pipeline.py`); keep `explain`.

---

## 13. Testing strategy

| Layer | Tests (owner) |
|---|---|
| **Migrations** | (A)<br>• Text-introspection tests in the style of `test_jobs_library_scope_migration.py`: the three stage CHECKs are dropped, the FK is added, the subject CHECK pairs each kind, the CONCURRENTLY + no-transaction directive is present, the SQLite rebuild keeps every index.<br>• Live Postgres (testcontainers or `DATABASE_URL`): apply 0001–0091; insert a `categorize` job (F6 regression); run each Go control UPDATE (F7 regression). |
| **Enqueue function** | (A, both dialects, table-driven, identical expectations)<br>• insert → coalesce (priority min, force OR, reasons capped at 20, debounce extends but is capped)<br>• running → `rerun_requested`<br>• disabled, unknown and invalid subject<br>• concurrent enqueue of 50 tasks yields one row |
| **Claim / pools** | (A)<br>• Global pool size 2 with 3 worker processes never exceeds 2 running (Postgres, real processes).<br>• Per-worker pool resize takes effect on the next drain.<br>• Paused stage and paused-all are skipped.<br>• Priority order is preserved. |
| **Propagation** | (A)<br>• Changed output enqueues exactly the declared edges; unchanged enqueues none.<br>• `force_cascade` reaches the leaves.<br>• A follow-up after a run that saw a mid-run trigger.<br>• An undeclared request raises.<br>• A crash between legacy `mark_done` and propagation is repaired by the reconciler.<br>• A version bump triggers one rerun per subject. |
| **Convergence** | (A)<br>• Property test (hypothesis) over random libraries: the detect ⇄ fp_gate ⇄ fingerprint loop reaches quiescence. Total jobs ≤ 3·|V| + 2·|seasons| + small constant; detect runs ≤ 3.<br>• Running the cascade twice adds zero changed outputs. |
| **Stages** | (A)<br>• Each new stage module is tested with fake integrations and fixture data ported from `tests/classify` and the mediaintel cache fixtures.<br>• Each stage's input fingerprint is stable across runs and changes on the documented inputs only. |
| **Import** | (A)<br>• Fixture cache → dry-run diff → apply.<br>• Every live job afterwards is `skipped_unchanged`; zero NAS reads (the fake `nas.timed` asserts it is never called). |
| **Causes** | (A) Golden table of real error strings (SMB "Invalid argument", WinError 121, yt-dlp bot check, Plex 401) → cause codes; `norm()` is idempotent. |
| **Go handlers** | (B)<br>• Unit tests on query builders and `waiting_reason` precedence (pure functions).<br>• `//go:build integration` tests against A's seed for every §8.1 route (golden JSON).<br>• Snapshot query under 20 ms on a 1M-row synthetic table (benchmark).<br>• SSE: `id:` line present; reconnect with `Last-Event-ID` replays the gap; `/ws/queues` emits within 2 s and stops publishing with zero subscribers. |
| **Web** | (C)<br>• Vitest + Testing Library per page and component with fixtures: ring maths, waiting-reason labels in en/ar, flows render in RTL, issue action calls the right endpoint, pool stepper bounds, 1 Hz batching, polling fallback only after 10 s of silence.<br>• Both themes: render with `data-theme="dark"` and assert no inline colours.<br>• An axe check on the three pages. |
| **End-to-end** | (all, Phase 3)<br>• Scan a fixture library of 40 videos.<br>• Expected cascade: probe → thumb_hash, link → detect → fingerprint samples → fp_gate → candidates → detect → tag, rate_style, plex (review) → plan → write (review).<br>• Assert job counts, then retrigger the library with `mode:"queue"` and assert everything is skipped. |

---

## 14. Performance considerations

- **Claim cost.** One advisory lock + one indexed UPDATE per claim. The busy count scans only live rows of one pool (partial-index sized). At a 10 ms claim, a skipped-unchanged storm of 50 k jobs drains in a few minutes without touching the NAS.
- **Event volume.** Skipped jobs emit no SSE and only debug `job_events` (7-day retention). A full library cascade of about 10 k real jobs produces about 30 k `events` rows, well within the bus's 7-day prune and its 500-row replay cap.
- **Snapshot cost.** Every 2 s per replica, only while someone watches. Count queries touch live rows and time windows only.
- **Hashing.** `detect`'s input fingerprint is one `string_agg(md5)` over the library's latest done rows. That is O(videos), uses the `subject_history` index and is about 50 ms for 10 k videos. It is the main cost of a no-op library re-queue.
- **NAS protection.** The `nas_read` default of 2 is the measured safe limit (`signatures.py:15-16`, `scan_visual.py:78`). It is global across hosts and processes, it is adjustable live, and its effect is visible through `nas.open_ms_p95` and `nas.timeouts`.

---

## 15. Risks and mitigations

| Risk | Mitigation |
|---|---|
| External writes (Plex, dizqueTV) fire automatically when a season changes. | D11 `review` default, with a per-stage switch. Writers keep the mediaintel safety rules: delete before recreate, read back and verify, never touch objects the tool did not create (`external_links.created_by_tool`). |
| `detect` input changes on every probe of a new file, causing re-detect churn during a big scan. | 30 s debounce, extended while triggers keep arriving, capped at 5 min (D7). |
| The SQLite 0088 rebuild on a large SQLite install. | SQLite installs are small (single-box). The rebuild copies with an explicit column list in one transaction, and a test runs it on 100 k rows. |
| Graph drift: an old worker runs with a new graph. | `graph_synced_at` + `version` in `job_stages`. Workers refuse to claim stages whose DB `version` is greater than their own, logging `graph_version_ahead`. The UI flags `stale_graph`. |
| Go and Python disagree on coalescing. | A single SQL function on Postgres (D8). SQLite has one Python implementation, checked by a shared golden test table. |
| `processing_jobs` growth from skipped rows. | Prune keeps only the latest done row per (subject, stage) after 90 days (§6.4). |
| An operator "Retry" on thousands of NAS timeouts while the NAS is still sick. | Retry-by-cause enqueues at priority 200. The issues panel shows the NAS health next to `nas_*` groups, and the action button is labelled with the current p95. |

---

## 16. Open questions

| # | Question | Default chosen |
|---|---|---|
| Q1 | `youtube_sync` and the legacy media stages are not in the approved overview mock-up. Render them as a Sources chip row and a collapsed Media lane? | yes |
| Q2 | Default `write_mode` for `plex_collection`, `dizquetv_show` and `write_channels`. | `review` |
| Q3 | Should video libraries that are YouTube archives skip the legacy audio chain (`extract → transcribe …`), which `probe` currently triggers by hand (F13)? This needs a per-library setting outside this plan. | unchanged behaviour |
| Q4 | `write_channels` targets Maktaba channels (primary) and dizqueTV (secondary) as two sub-steps of one stage. Or should dizqueTV be its own card? | one stage, two sub-steps |
| Q5 | `catalog` vs the brief's "collection" naming (D2). | `catalog` |

Additional finding, not fixed here: the comment in `0058_…sqlite.sql:11-12` claims SQLite 3.35+ supports `ADD COLUMN IF NOT EXISTS`. SQLite has no `IF NOT EXISTS` form for `ADD COLUMN`, so that twin likely fails to apply on a real SQLite. It is only text-tested today. The 0088 rebuild supersedes it on fresh installs, but A should confirm by applying 0001–0058 SQLite twins in a test and fix any failing twin with a follow-up slot.

### Workstream C notes

Contradictions and gaps found while building the web UI, with the option chosen.

| # | Finding | Chosen |
|---|---------|--------|
| C1 | §9.1 asks for dark values of success/warn/error; the existing light success (4.1:1) and warn (3.6:1) also fail AA on `--color-bg`. | Darkened light success/warn too (`#0b6e3f`, `#8f5600`). Added `--color-running` (saffron) and `--color-info` (blue). A dark-only override keeps the `#fff` label rule in `app.css` readable on the brighter dark fills. |
| C2 | Health tiles need a 4-hour sparkline; `HealthNow.spark` carries only 30 one-minute samples. | Tiles use `GET /api/queues/health?hours=4` (refreshed every 60 s), and fall back to `spark`. Metric names assumed: `nas.open_ms_p95`, `cpu.pct`, `net.plex_ms`, `backlog.ready` (backlog is not a sampled metric today, so it uses `spark`). |
| C3 | Queue page KPI "failed retrying vs gave up" and "avg" have no contract fields. | Shows `counts.failed_open` as "gave up", `kpis.failed_24h`, median and p95. |
| C4 | §9.2 RetriggerDialog says "search library, season or video", but no search endpoint exists. | Searches the libraries list; seasons load from the library item history; videos arrive only prefilled (item page). |
| C5 | Decision: external writes are automatic. | Fixtures default `write_mode` to `apply`; no review issue by default. The Apply action and write-mode select still work if B returns an `apply` group. `TimelineEntry.changes?: WriteChange[]` (added/removed/reordered/created/deleted, with items) is proposed for the item history; B must fill it from `job_events.data`. |
| C6 | Decision: Plex and dizqueTV are extensions. | `StageInfo.extension` (null = core); extension queues render in an "Extensions" lane after Channels, with a badge. Extension APIs assumed: `GET /api/extensions`, `PUT /api/extensions/{name}`, `POST /api/extensions/{name}/test`. Item page hides Plex and dizqueTV "Where it appears" rows unless the extension is enabled. |
| C7 | Decision: workers on several machines. | `GET /api/workers` -> `{workers: [{worker_id, host, online, last_seen_at, serves[], serves_label}]}`; `QueueSummary.served_by?` and `no_worker?`. `no_worker` with waiting jobs shows "Waiting for a worker that serves <stage>". The Media lane opens by default when it has work. |

### Workstream B notes

Decisions and deviations made while implementing workstream B (Go API + SSE). Where this plan and `web/src/lib/queueApi.ts` disagreed, the TypeScript file won, as agreed with the coordinator.

1. **Event bus path.** The plan names `api/internal/eventbus/bus.go`. The bus actually lives in `api/internal/handlers/ws/eventbus/`, and `Event.toHubEvent` already injects `_event_id` into live events, so no change to `bus.go` was needed. The missing piece was the SSE writer, which now emits `id:` lines (`writeFrame` in `ws.go`) for both live and replayed frames.
2. **SSE headers are flushed on connect.** Before this, `EventSource.onopen` waited for the first event or the 30 s ping.
3. **`/ws/queues` initial frame.** `ws.Handler.Initial` (a hook) sends a fresh snapshot on connect. `queues.Handler.PublishSnapshots` publishes every 2 s only while `Hub.SubscriberCount("queues") > 0`. It starts from `MountP6` only when `P6Deps.BusCtx` is set, so tests that mount without a context do not leak a goroutine.
4. **Enqueue helper.** `queues.Enqueue` (new file `queues/enqueue.go`) is the single Go call site of `processing_jobs_enqueue`. `libraries/enqueuer.go`, `jobs.Retry`, retrigger, retry-by-cause and apply all use it.
5. **Tests do not need A's migrations.** Postgres is not available in the build environment, so unit tests run on a scripted `database/sql` driver (`queues/queuestest`). It checks which statements run, their arguments and the row-to-JSON mapping, not SQL validity. `api/queues_integration_test.go` (`-tags=integration`, needs `DATABASE_URL`) applies the real migrations and skips itself while 0087-0090 are absent. **The SQL has therefore not been executed against Postgres yet.**
6. **`write_mode`.** Per the user's decision external writes run automatically: values `auto` (default, set by A), `apply` (accepted as a synonym) and `review`. `PATCH` accepts all three. The `apply` endpoint is kept as specified. `queueApi.ts` types the field as `"apply" | "review"`, so C should widen it to include `"auto"`.
7. **Workers registry (assumed shape).** `workers(id TEXT PK, host TEXT, stages TEXT[], pools JSONB, started_at, last_seen_at, version TEXT)`. A worker is online when `last_seen_at` is within 60 s. `GET /api/workers` returns `worker_id, host, online, last_seen_at, serves[], serves_label` (plus `pools`, `started_at`, `version`). `QueueSummary.served_by` is the list of online hosts serving the stage (as in `queueApi.ts`; the plan had no such field), with `served_workers` as an extra count. `no_worker` is true when the stage is enabled, has pending work and no online worker serves it. It also becomes waiting reason `no_worker` (after `awaiting_review`, before `pool_full`). `/api/queue/stats` carries `served_workers`, `served_hosts`, `no_worker` and `extension` per stage and tolerates a missing `workers` table.
8. **Extensions (assumed shape).** `extensions(name TEXT PK, enabled BOOLEAN, settings JSONB, updated_at)` and `job_stages.extension TEXT NULL`. The catalogue (label, version, description, settings schema for `plex` and `dizquetv`) is code in `queues/extensions.go`. Only schema keys are ever echoed; secrets come back as `********` plus `secrets_set`. A client that sends the mask back keeps the stored secret, and `null` or `""` clears it. `POST /api/extensions/{name}/test` answers `200 {ok:false, message:"not_implemented: ..."}` (the shape `queueApi.ts` expects) instead of 501.
9. **`stale_graph` heuristic.** The API cannot see worker handlers. A stage is reported as `stale_graph` when it is enabled and not paused, has ready pending rows, and `graph_synced_at` is NULL or older than 24 h.
10. **`issues_count`** is the number of issue groups (failure causes + awaiting_review + stale_graph), not the number of failed subjects.
11. **`backlog.ready` health series.** Workers do not sample the backlog. The snapshot publisher inserts one `health_samples(metric='backlog.ready', host='api')` row per minute (`ON CONFLICT DO NOTHING`) while someone watches `/ws/queues`; `GET` handlers never write. `spark.backlog_ready` in the snapshot comes from an in-memory ring (one sample per minute, lost on restart).
12. **Timeline `changes`.** `TimelineEntry.changes` is read from `job_events` rows of kind `applied` or `review` (`data.changes`), falling back to `output_summary.changes`; `output_summary` itself is also returned. Entry shape: `{action, what, target, count?, items?}` as in `queueApi.ts`.
13. **History `kinds` for a video** are derived from `series_episodes` membership and duration (`episode`, `short`, `unassigned`); `extra` is produced for a video listed in a live season's `series_seasons.metadata.extras`, which `detect` writes as a JSON array of video ids (UUID strings; `attached` and `duplicates` use the same shape). The lookup is `metadata->'extras' @> to_jsonb(video_id::text)` and is checked before episode membership.
14. **Reads need only an authenticated principal.** Every mutation needs `IsAdmin` (403 otherwise). Library ACLs are not applied to queue views.
15. **Library strip.** `done` counts distinct subjects with a done row (capped at `total`); hidden stages are left out; the endpoint is cached 5 s in memory per replica. `GET /api/queues/{stage}/jobs` pages newest first by id (cursor = last id).
16. **Retry-by-cause** re-enqueues at priority 200 (plan section 15), capped at 20000 subjects per call. Legacy rows with no `error_cause` are grouped by the JSON `kind` of their error text (`unknown` for plain text) and merge with coded groups of the same name.
17. **Stage resume** clears `pause_requested` only for cooperative stages (`transcribe`), matching what `pause?interrupt=true` sets.
18. **Not done:** 1M-row snapshot benchmark (a `BenchmarkQueuesSnapshot` exists but needs a loaded database); the snapshot/detail SQL is unverified on Postgres (see item 5); A's `queue_seed.sql` was not available, so the integration test only loads it if present.

## Workstream A notes

Decisions and deviations recorded by workstream A (DB + Python engine). Newest items last.

**User decisions, binding over the sections above**

- **N1. External writes run automatically.** `plex_collection`, `dizquetv_show` and `write_channels` apply their changes without a review step. This overrides D11, Q2 and the "review by default" seeds. `job_stages.write_mode` is kept, with values `auto` (default, seeded for every stage) and `review` (an unused option for now). Every write handler computes a `Diff` value first (added, removed, reordered), applies it, and stores that same diff in a `job_events` row of kind `applied` (and in `output_summary`). When the approval workflow (notifications, review inbox, approve/reject, audit) is built later as a separate feature, `review` mode will store the diff as a review item instead of applying it. The seam is the `compute_diff` / `apply_diff` split in each write handler. B and C: `write_mode` is `"auto" | "review"`, not `"apply" | "review"`, and the "Needs attention: Apply" flow is not built now.
- **N2. Multi-host workers.** The audio chain (`extract -> transcribe -> subtitle_gen/index`) runs on a Mac worker, not the Windows machine. Every worker process declares its host, the stages it serves, its pools and its own path map (NAS as `Y:/` and `O:/` on Windows, `/Volumes/...` on the Mac; the database keeps POSIX paths). Workers register in the new `workers` table (slot 0089: `id, host, stages, pools, path_map, version, started_at, last_seen_at`) and heartbeat into `last_seen_at`. A job is only offered to a worker that serves its stage. A stage with no live worker is reported by `engine.workers.stages_without_worker()`; B should surface it as a waiting reason ("waiting for a worker that serves transcribe"). The `gpu` pool belongs to the Mac host; `nas_read` stays a global, cross-host limit. YouTube libraries get the audio chain too (open question Q3 is resolved: no per-library skip).

**Deviations**

- **D-A1. `video_tags` name clash.** Slot 0034 already owns `video_tags (video_id, tag_id)`. The auto-tag table for loose videos is named `video_topic_tags (video_id, tag, score)`. `season_tags` is as specified.
- **D-A2. `external_links.subject_id` is TEXT**, not UUID, because channel links use the plan's channel key.
- **D-A3. Rollout bridge trigger.** Slot 0088 adds a BEFORE INSERT trigger (`processing_jobs_fill_subject_trg`, AFTER INSERT on SQLite) that fills `subject_kind/subject_id` from `video_id/library_id`, so old INSERT paths keep working until slot 0091. Without it the new `subject_chk` would reject every legacy writer.
- **D-A4. Library backfill.** 0088 also backfills `library_id` on per-video rows (the libraries table in B reads it).
- **D-A5. Payload merge.** `processing_jobs_enqueue` applies the `payload.seasons` set-union on every coalesce, including onto a running row (the follow-up row then inherits the superset); other payload keys keep the first value.
- **D-A6. Edge map `payload_seasons`** is added to the `Edge.map` literal (section 3.3 lists it as a row but not in the literal).
- **D-A7. Handlers are bound lazily.** `StageDef.run`, `input_fingerprint` and `output_fingerprint` stay `None` in `engine/graph.py` and are bound from `engine/stages/registry.py` (`bound_graph()`), so importing the graph never imports ffmpeg or HTTP code.
- **D-A8. SQLite legacy twins.** Stock SQLite 3.42 rejects `ADD COLUMN IF NOT EXISTS` (slots 0007, 0008, 0010, 0045, 0048, 0054, 0058, 0065, 0068, 0072, 0076, 0078) and slot 0004's twin references `last_seen_at` before 0007 adds it. The test harness applies legacy slots with a PRAGMA-guarded rewrite and tolerates the 0004 failure; slots 0087 and later must apply unmodified, and do. Fixing the legacy twins needs a consolidated SQLite baseline, not a follow-up slot.
- **N1a. The change-set seam covers Maktaba's own catalog too.** Besides the external writes, every stage that changes user-visible structure inside Maktaba follows the same shape: `detect` (series and season membership, order, kind including ongoing/year seasons, extras), `tag` (season and video tags), `rate_style`, `plan_channels` and `write_channels` (Maktaba Live TV lineups). Each such handler has a pure `compute_changes(...)` returning a typed, JSON-serialisable `ChangeSet` against current state, and a separate `apply_changes(...)`; the handler runs both, and stores the change set in the job's `job_events` row (kind `applied`, `data.changes`) and `output_summary`. `write_mode` defaults to `auto` for every stage. A later `review` mode stores the change set as a pending review item instead of calling `apply_changes`. Pure-compute stages (`probe`, `thumb_hash`, `fingerprint`, `style_sample`, `link`) are exempt. No review flow is built now.

**N3. Plex and dizqueTV are extensions, not core (user decision).** Core is Maktaba's own library, the analysis stages, and Live TV (`plan_channels` plus `write_channels`, which writes Maktaba channels only). Plex and dizqueTV live under `maktaba_pipeline/extensions/<name>/` and core never imports them.

- **Extension contract** (`extensions/base.py`): a `Manifest` (name, version, description, settings schema with secret keys flagged, stage names it contributes) and `register(graph: GraphBuilder)`, which adds the extension's `StageDef`s (tagged with `extension=<name>`, pools `network_plex` / `network_tv`) and wires them in by adding edges from core stages (`detect -> plex_collection` per changed season, `plan_channels -> dizquetv_channels`, ...). The core `GRAPH` therefore names no extension stage; `engine/loader.py` builds the full graph.
- **Discovery:** built-in extensions are listed in code (`plex`, `dizquetv`); third parties can ship an entry point in the group `maktaba.extensions`.
- **State:** table `extensions (name, enabled, settings JSON, updated_at)` (slot 0089). Env vars (`MAKTABA_PLEX_URL`, `MAKTABA_PLEX_TOKEN`, `MAKTABA_DIZQUETV_URL`, `MAKTABA_PATH_MAP`) are only the bootstrap defaults for the first row. Secrets are flagged in the manifest and must never be returned by the API. Plex's own path map (Plex's view of paths) is an extension setting.
- **Registry columns:** `job_stages.extension` (NULL for core) lets the API and UI badge and group extension stages. A disabled extension registers its stages with `enabled=false, disabled_reason='extension_disabled'`; an enabled but unconfigured one with `disabled_reason='not_configured'`. Both are visible in the UI and never claimed. The graph sync may flip `extension_disabled` and `not_configured` (never `operator`).
- **Stages:** `plex_collection` and `dizquetv_show` (season subjects) and `dizquetv_channels` (catalog subject, triggered by the `plan_channels` output) belong to the extensions and are not in the 0087 seed; they appear in `job_stages` when a worker syncs. `external_links.target` is therefore an open vocabulary (CHECK removed).
- B and C: group by `StageInfo.extension`; a stage may disappear from the graph when an extension is removed.

**More deviations and decisions (workstream A, implementation)**

- **D-A9. Stage enums.** `domain/states.py::Stage` is the video state machine's stage set (its transition tables are keyed by it), so the 0049 names are *not* added there; doing so breaks 36 FSM tests for no gain. `db/jobs.py::Stage` also stays the original seven (a test pins that it matches the concurrency defaults). `Job.stage` is typed `str`; a legacy stage comes back as the `Stage` member (identity comparisons keep working), anything else as a plain string. The registry is `job_stages`.
- **D-A10. Engine stages are bound by the registry, not `handlers.build_real_dispatch`.** `handlers.py` only gained `to_local()` path mapping for probe and extract. `thumbnail/handler.py` (not in A's file list) still reads `videos.path` unmapped, so the thumbnail stage needs a mount at the DB path on the host that runs it.
- **D-A11. `runtime.Database`** is now transaction-scoped per task (a nested `transaction()` joins; Postgres uses a savepoint), gained `fetch`/`fetchval`, runs SQLite in autocommit with `BEGIN IMMEDIATE`, and serialises tasks on the single SQLite connection. `_rewrite_for_sqlite` passes SQL that already uses `?` through unchanged (the SQLite twins in `jobs_state.py` never worked through the facade before).
- **D-A12. Tests of replaced behaviour.** `tests/db/test_jobs_enqueue.py` now runs against the real schema (the old in-memory fake modelled the removed INSERT); `tests/audio/_fake_audio_db.py` learned the `processing_jobs_enqueue` call. The old pool-unaware loop and `claim_one` stay (worker `pool_mode=False`), because their tests and `--no-engine` use them.
- **D-A13. `thumb_hash` fingerprint is DB-only** (size, mtime, poster path), as D5 demands, so a thumbnail sidecar that appears later without the video changing is only noticed on a forced run.
- **D-A14. `detect` groups by folder** (one folder = one channel, as the prototype), runs the detector in a thread, samples the fingerprint plan from the seed members (a sample set that moves when candidates attach would re-read the NAS forever), and reads `Season.numbers` when the detector provides it (falls back to 1..n). Season ids that collide across folders get the folder appended to the series key.
- **D-A15. Channel plan.** Episode and filler `path` in `channel_plans.plan` is the Maktaba video id; `plex_rating_key` is null in core. Extensions add per-channel data through `register_plan_exporter` (`channels[i].exports.<name>`); plan format 2.
- **D-A16. `write_channels` safety.** Only channels recorded in `external_links(maktaba_channel)` with `created_by_tool` are updated or removed; a row whose content no longer matches the stored hash (the user edited it) is skipped and reported; a number or slug held by a foreign channel is a reported conflict. Its input fingerprint is the latest plan row id.
- **D-A17. `link`** matches against all YouTube videos of the library's channels (a library with several channels shares one pool; duplicate-title ambiguity is resolved by the existing duration rules and the taken-id set). The candidate index is cached per library.
- **D-A18. Reconciler** schedules the YouTube refresh only for libraries that have `youtube_channels` rows. Version sweeps skip legacy stages and run at priority 500, 1000 subjects per stage per pass.
- **D-A19. Import tool** does not import `probe` (Maktaba probes its own files) or extension stages (an extension reconciles with its server on first run; its `plex_collection` link is imported) or `plan_channels` / `write_channels` (derived). It writes inside one transaction (dry run rolls it back), reuses `detect.apply_changes` so ids and membership hashes match the live stage, and sets `season_override` / `episode_override` for the cache's user-override rows.
- **D-A20. Property tests** use seeded `random` loops, not hypothesis (not installed, and `pyproject.toml` is outside A's file list).
- **D-A21. Postgres in tests.** No Postgres ships with the repo's test tier; the Postgres parametrisations run when `MAKTABA_TEST_PG_URL` points at a server (each pytest process builds a migrated template database once, about 40 s) and skip otherwise. They were all run against PostgreSQL 16 during development, including the multi-process pool test.
- **D-A22. From the extension work.** A Plex collection or dizqueTV show with the same name that the tool did not create is never replaced (the stage reports a `title_conflict` / `name_conflict` and finishes `waiting_inputs`); the prototype's replace-existing and remove-superseded flags and its updating of hand-named dizqueTV shows are dropped. The first `plex_collection` run caches Plex rating keys, so its input fingerprint changes once and a second run follows (then `skipped_unchanged`).
- **D-A23. Bootstrap, extension settings.** `extensions` rows are created from the environment on first sight (enabled only when configured); afterwards the database wins and API responses must use `extensions.store.public_settings` (secrets masked as `********`, a masked value on save keeps the stored secret).
- **D-A24. CLI.** `python -m maktaba_pipeline run` gained `--exclude-stages`, `--host`, `--path-map` (this host's mounts, `db_prefix=local_prefix;...`) and `--no-engine`; with no `--stages` a worker serves every stage it has a handler for. A Mac worker is started with the audio chain and its path map; the Windows worker excludes it (`--exclude-stages extract,transcribe,subtitle_gen,index`).
